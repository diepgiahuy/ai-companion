#pragma once

#include "companion/app.hpp"

#include <algorithm>
#include <array>
#include <cstddef>
#include <cstdint>
#include <span>

namespace companion {

struct AudioRuntimeStats {
  uint64_t playback_epoch{};
  uint64_t playback_starts{};
  uint64_t playback_stops{};
  uint64_t accepted_playback_samples{};
  uint64_t reference_epochs{};
  uint64_t reference_push_failures{};
  uint64_t unsupported_reference_rates{};
  uint64_t oversized_reference_frames{};
  uint64_t reference_converter_dropped_groups{};
  uint64_t invalid_playback_writes{};
  bool capture_active{};
  bool playback_active{};
  bool reference_active{};
  bool frontend_enabled{};
  PlaybackReferenceStats frontend_reference{};
};

// Board-facing capture port. Only AudioRuntime should compose this into the
// product runtime; CompanionApp must not own physical audio adapters directly.
class Microphone {
public:
  virtual ~Microphone() = default;
  virtual bool start_capture() = 0;
  virtual size_t read_capture(std::span<int16_t> destination) = 0;
  virtual void stop_capture() = 0;
};

// Board-facing playback port. Accepted-frame semantics are the authoritative
// source for the AEC reference owned by AudioRuntime.
class Speaker {
public:
  virtual ~Speaker() = default;
  virtual bool start_playback(uint32_t sample_rate_hz) = 0;
  virtual size_t write_playback(std::span<const int16_t> mono_pcm) = 0;
  virtual bool playback_drained() const = 0;
  virtual void stop_playback() = 0;
};

// The only app-facing audio owner. Physical microphone/speaker adapters and the
// optional vendor AFE remain private implementation details below this boundary.
// A no-AFE runtime is supported for host/simulator/manual-VAD paths without
// reintroducing separate audio ports into CompanionApp.
class AudioRuntime final : public AudioEngine {
public:
  AudioRuntime(Microphone& microphone, Speaker& speaker)
      : microphone_(microphone), speaker_(speaker) {}

  AudioRuntime(Microphone& microphone, Speaker& speaker, AudioFrontend& frontend)
      : microphone_(microphone), speaker_(speaker), frontend_(&frontend) {}

  bool start() override {
    stop_capture();
    stop_playback();
    reference_converter_.reset();
    reference_source_rate_hz_ = 0;
    if (frontend_ == nullptr) return true;
    frontend_->reset();
    return frontend_->start();
  }

  void reset() override {
    end_current_reference();
    reference_converter_.reset();
    reference_source_rate_hz_ = 0;
    if (frontend_ != nullptr) frontend_->reset();
  }

  bool frontend_enabled() const override { return frontend_ != nullptr; }

  bool set_wake_threshold(float threshold) override {
    // Host/manual-VAD configurations have no acoustic detector to update, so
    // there is no hidden physical state to diverge from the app config.
    if (frontend_ == nullptr) return true;
    return frontend_->set_wake_threshold(threshold);
  }

  bool start_capture() override {
    if (capture_active_) return true;
    if (!microphone_.start_capture()) return false;
    capture_active_ = true;
    return true;
  }

  size_t read_capture(std::span<int16_t> destination) override {
    if (!capture_active_) return 0;
    return microphone_.read_capture(destination);
  }

  void stop_capture() override {
    if (!capture_active_) return;
    microphone_.stop_capture();
    capture_active_ = false;
  }

  bool start_playback(uint32_t sample_rate_hz) override {
    if (playback_active_) return false;
    end_current_reference();
    reference_converter_.reset();
    reference_source_rate_hz_ = 0;
    if (!speaker_.start_playback(sample_rate_hz)) return false;
    playback_active_ = true;
    playback_epoch_ = next_epoch(playback_epoch_);
    ++playback_starts_;
    return true;
  }

  size_t write_playback(std::span<const int16_t> mono_pcm) override {
    if (!playback_active_) {
      ++invalid_playback_writes_;
      return 0;
    }
    const size_t accepted = speaker_.write_playback(mono_pcm);
    if (accepted > mono_pcm.size()) {
      ++invalid_playback_writes_;
      return accepted;
    }
    accepted_playback_samples_ += accepted;
    return accepted;
  }

  bool playback_drained() const override {
    return !playback_active_ || speaker_.playback_drained();
  }

  void stop_playback() override {
    end_current_reference();
    reference_converter_.reset();
    reference_source_rate_hz_ = 0;
    if (!playback_active_) return;
    speaker_.stop_playback();
    playback_active_ = false;
    ++playback_stops_;
  }

  bool push_playback_reference(std::span<const int16_t> accepted_pcm,
                               uint32_t sample_rate_hz) override {
    if (accepted_pcm.empty() || frontend_ == nullptr) return true;
    if (!playback_active_) return reference_failure();
    if (accepted_pcm.size() > reference_frame_.size()) {
      ++oversized_reference_frames_;
      return reference_failure();
    }
    if (sample_rate_hz != 16'000 && sample_rate_hz != 24'000) {
      ++unsupported_reference_rates_;
      return reference_failure();
    }
    if (reference_active_ && reference_source_rate_hz_ != sample_rate_hz) {
      ++unsupported_reference_rates_;
      return reference_failure();
    }
    if (!reference_active_) {
      reference_converter_.reset();
      reference_source_rate_hz_ = sample_rate_hz;
      if (!begin_playback_reference(playback_epoch_)) {
        reference_source_rate_hz_ = 0;
        return reference_failure();
      }
    }

    if (sample_rate_hz == 16'000) {
      if (!frontend_->push_playback_reference(accepted_pcm, 16'000)) {
        return reference_failure();
      }
      return true;
    }

    const uint64_t dropped_before = reference_converter_.dropped_groups();
    const size_t converted = reference_converter_.convert(accepted_pcm, reference_frame_);
    if (reference_converter_.dropped_groups() != dropped_before) {
      return reference_failure();
    }
    if (converted == 0) return true; // converter may be carrying 1–2 samples.
    if (!frontend_->push_playback_reference(
            std::span<const int16_t>(reference_frame_.data(), converted), 16'000)) {
      return reference_failure();
    }
    return true;
  }

  AudioFrontendResult process_capture(std::span<const int16_t> microphone_16k,
                                      std::span<int16_t> cleaned_16k) override {
    if (frontend_ != nullptr) {
      return frontend_->process_capture(microphone_16k, cleaned_16k);
    }
    const size_t count = std::min(microphone_16k.size(), cleaned_16k.size());
    std::copy_n(microphone_16k.begin(), count, cleaned_16k.begin());
    return {.samples = count, .event = AudioFrontendEvent::none};
  }

  AudioRuntimeStats stats() const {
    return {
        .playback_epoch = playback_epoch_,
        .playback_starts = playback_starts_,
        .playback_stops = playback_stops_,
        .accepted_playback_samples = accepted_playback_samples_,
        .reference_epochs = reference_epochs_,
        .reference_push_failures = reference_push_failures_,
        .unsupported_reference_rates = unsupported_reference_rates_,
        .oversized_reference_frames = oversized_reference_frames_,
        .reference_converter_dropped_groups = reference_converter_.dropped_groups(),
        .invalid_playback_writes = invalid_playback_writes_,
        .capture_active = capture_active_,
        .playback_active = playback_active_,
        .reference_active = reference_active_,
        .frontend_enabled = frontend_ != nullptr,
        .frontend_reference = frontend_ == nullptr ? PlaybackReferenceStats{}
                                                   : frontend_->playback_reference_stats(),
    };
  }

private:
  static uint64_t next_epoch(uint64_t current) {
    ++current;
    return current == 0 ? 1 : current;
  }

  bool reference_failure() {
    ++reference_push_failures_;
    return false;
  }

  bool begin_playback_reference(uint64_t epoch) {
    if (frontend_ == nullptr || !playback_active_ || epoch == 0 || epoch != playback_epoch_) {
      return false;
    }
    if (reference_active_) return reference_epoch_ == epoch;
    if (!frontend_->begin_playback_reference(epoch)) return false;
    reference_active_ = true;
    reference_epoch_ = epoch;
    ++reference_epochs_;
    return true;
  }

  void end_current_reference() {
    if (reference_active_ && frontend_ != nullptr) {
      frontend_->end_playback_reference(reference_epoch_);
    }
    reference_active_ = false;
    reference_epoch_ = 0;
    reference_converter_.reset();
    reference_source_rate_hz_ = 0;
  }

  Microphone& microphone_;
  Speaker& speaker_;
  AudioFrontend* frontend_{};
  PlaybackReference24To16 reference_converter_{};
  std::array<int16_t, kAudioFrameSamples> reference_frame_{};
  bool capture_active_{};
  bool playback_active_{};
  bool reference_active_{};
  uint32_t reference_source_rate_hz_{};
  uint64_t playback_epoch_{};
  uint64_t reference_epoch_{};
  uint64_t playback_starts_{};
  uint64_t playback_stops_{};
  uint64_t accepted_playback_samples_{};
  uint64_t reference_epochs_{};
  uint64_t reference_push_failures_{};
  uint64_t unsupported_reference_rates_{};
  uint64_t oversized_reference_frames_{};
  uint64_t invalid_playback_writes_{};
};

} // namespace companion
