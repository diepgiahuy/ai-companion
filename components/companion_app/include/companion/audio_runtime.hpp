#pragma once

#include "companion/app.hpp"

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
  uint64_t invalid_playback_writes{};
  bool capture_active{};
  bool playback_active{};
  bool reference_active{};
  PlaybackReferenceStats frontend_reference{};
};

// One production owner for physical capture, physical playback and the AFE.
// CompanionApp still speaks the existing Microphone/Speaker/AudioFrontend ports
// during the #228 migration, but all three ports resolve to this object so
// lifetime/epoch invariants are enforced in one place rather than by unrelated
// board adapters.
class AudioRuntime final : public Microphone, public Speaker, public AudioFrontend {
public:
  AudioRuntime(Microphone& microphone, Speaker& speaker, AudioFrontend& frontend)
      : microphone_(microphone), speaker_(speaker), frontend_(frontend) {}

  bool start() override {
    end_current_reference();
    capture_active_ = false;
    playback_active_ = false;
    return frontend_.start();
  }

  void reset() override {
    end_current_reference();
    frontend_.reset();
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
    if (!playback_active_) return;
    speaker_.stop_playback();
    playback_active_ = false;
    ++playback_stops_;
  }

  bool begin_playback_reference(uint64_t epoch) override {
    if (!playback_active_ || epoch == 0 || epoch != playback_epoch_) return false;
    if (reference_active_) return reference_epoch_ == epoch;
    if (!frontend_.begin_playback_reference(epoch)) return false;
    reference_active_ = true;
    reference_epoch_ = epoch;
    ++reference_epochs_;
    return true;
  }

  void end_playback_reference(uint64_t epoch) override {
    if (!reference_active_ || epoch == 0 || epoch != reference_epoch_) return;
    frontend_.end_playback_reference(epoch);
    reference_active_ = false;
    reference_epoch_ = 0;
  }

  bool push_playback_reference(std::span<const int16_t> pcm_16k) override {
    if (pcm_16k.empty()) return true;
    if (!playback_active_) {
      ++reference_push_failures_;
      return false;
    }
    if (!reference_active_ && !begin_playback_reference(playback_epoch_)) {
      ++reference_push_failures_;
      return false;
    }
    if (!frontend_.push_playback_reference(pcm_16k)) {
      ++reference_push_failures_;
      return false;
    }
    return true;
  }

  PlaybackReferenceStats playback_reference_stats() const override {
    return frontend_.playback_reference_stats();
  }

  AudioFrontendResult process_capture(std::span<const int16_t> microphone_16k,
                                      std::span<int16_t> cleaned_16k) override {
    return frontend_.process_capture(microphone_16k, cleaned_16k);
  }

  AudioRuntimeStats stats() const {
    return {
        .playback_epoch = playback_epoch_,
        .playback_starts = playback_starts_,
        .playback_stops = playback_stops_,
        .accepted_playback_samples = accepted_playback_samples_,
        .reference_epochs = reference_epochs_,
        .reference_push_failures = reference_push_failures_,
        .invalid_playback_writes = invalid_playback_writes_,
        .capture_active = capture_active_,
        .playback_active = playback_active_,
        .reference_active = reference_active_,
        .frontend_reference = frontend_.playback_reference_stats(),
    };
  }

private:
  static uint64_t next_epoch(uint64_t current) {
    ++current;
    return current == 0 ? 1 : current;
  }

  void end_current_reference() {
    if (!reference_active_) return;
    frontend_.end_playback_reference(reference_epoch_);
    reference_active_ = false;
    reference_epoch_ = 0;
  }

  Microphone& microphone_;
  Speaker& speaker_;
  AudioFrontend& frontend_;
  bool capture_active_{};
  bool playback_active_{};
  bool reference_active_{};
  uint64_t playback_epoch_{};
  uint64_t reference_epoch_{};
  uint64_t playback_starts_{};
  uint64_t playback_stops_{};
  uint64_t accepted_playback_samples_{};
  uint64_t reference_epochs_{};
  uint64_t reference_push_failures_{};
  uint64_t invalid_playback_writes_{};
};

} // namespace companion
