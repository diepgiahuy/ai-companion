#include "companion/audio_runtime.hpp"

#include <algorithm>
#include <array>
#include <cassert>
#include <span>
#include <vector>

using namespace companion;

namespace {
struct FakeMicrophone final : Microphone {
  bool active{};
  size_t starts{};
  size_t stops{};
  bool start_capture() override { active = true; ++starts; return true; }
  size_t read_capture(std::span<int16_t> destination) override {
    if (!active || destination.empty()) return 0;
    destination[0] = 123;
    return 1;
  }
  void stop_capture() override { if (active) ++stops; active = false; }
};

struct PartialSpeaker final : Speaker {
  bool active{};
  size_t starts{};
  size_t stops{};
  size_t max_accept{2};
  bool start_playback(uint32_t) override {
    if (active) return false;
    active = true;
    ++starts;
    return true;
  }
  size_t write_playback(std::span<const int16_t> pcm) override {
    if (!active) return 0;
    return std::min(max_accept, pcm.size());
  }
  bool playback_drained() const override { return active; }
  void stop_playback() override { if (active) ++stops; active = false; }
};

struct FakeFrontend final : AudioFrontend {
  bool started{};
  bool active{};
  uint64_t epoch{};
  uint32_t last_reference_rate{};
  size_t starts{};
  size_t begins{};
  size_t ends{};
  size_t resets{};
  std::vector<int16_t> references;
  PlaybackReferenceStats stats{};

  bool start() override { started = true; ++starts; return true; }
  void reset() override { ++resets; active = false; epoch = 0; stats.active = false; stats.epoch = 0; }
  bool begin_playback_reference(uint64_t value) override {
    if (value == 0 || active) return false;
    active = true;
    epoch = value;
    ++begins;
    stats.active = true;
    stats.epoch = value;
    return true;
  }
  void end_playback_reference(uint64_t value) override {
    if (!active || value != epoch) return;
    active = false;
    epoch = 0;
    ++ends;
    stats.active = false;
    stats.epoch = 0;
  }
  bool push_playback_reference(std::span<const int16_t> pcm,
                               uint32_t sample_rate_hz) override {
    if (!active || sample_rate_hz != 16'000) return false;
    last_reference_rate = sample_rate_hz;
    references.insert(references.end(), pcm.begin(), pcm.end());
    stats.pushed_samples += pcm.size();
    return true;
  }
  PlaybackReferenceStats playback_reference_stats() const override { return stats; }
  AudioFrontendResult process_capture(std::span<const int16_t> input,
                                      std::span<int16_t> output) override {
    const size_t count = std::min(input.size(), output.size());
    std::copy_n(input.begin(), count, output.begin());
    return {.samples = count};
  }
};
} // namespace

int main() {
  FakeMicrophone microphone;
  PartialSpeaker speaker;
  FakeFrontend frontend;
  AudioRuntime runtime(microphone, speaker, frontend);

  assert(runtime.start());
  assert(frontend.started);
  assert(frontend.starts == 1);
  assert(frontend.resets == 1);

  assert(runtime.start_capture());
  assert(runtime.start_capture());
  assert(microphone.starts == 1);
  std::array<int16_t, 4> capture{};
  assert(runtime.read_capture(capture) == 1);
  runtime.stop_capture();
  runtime.stop_capture();
  assert(microphone.stops == 1);

  assert(runtime.start_playback(24'000));
  const auto first_epoch = runtime.stats().playback_epoch;
  assert(first_epoch != 0);
  assert(!runtime.stats().reference_active);

  const std::array<int16_t, 4> pcm{10, 20, 30, 40};
  speaker.max_accept = 3;
  const size_t accepted = runtime.write_playback(pcm);
  assert(accepted == 3);
  assert(runtime.stats().accepted_playback_samples == 3);
  assert(frontend.begins == 0);

  // Caller supplies only the physical TX-accepted prefix at its source rate.
  // Runtime owns the bounded 24k -> 16k conversion and vendor frontend sees
  // only the AFE-native rate.
  assert(runtime.push_playback_reference(
      std::span<const int16_t>(pcm.data(), accepted), 24'000));
  assert(frontend.begins == 1);
  assert(runtime.stats().reference_active);
  assert(runtime.stats().reference_epochs == 1);
  assert(frontend.last_reference_rate == 16'000);
  assert((frontend.references == std::vector<int16_t>{10, 25}));
  assert(runtime.stats().reference_converter_dropped_groups == 0);

  // Source rate is fixed for one reference epoch; silently changing it would
  // corrupt streaming converter phase/alignment.
  const std::array<int16_t, 2> direct16{7, 8};
  assert(!runtime.push_playback_reference(direct16, 16'000));
  assert(runtime.stats().unsupported_reference_rates == 1);
  assert(runtime.stats().reference_push_failures == 1);

  runtime.stop_playback();
  assert(frontend.ends == 1);
  assert(!runtime.stats().reference_active);
  assert(runtime.stats().playback_stops == 1);
  assert(!runtime.push_playback_reference(direct16, 16'000));
  assert(runtime.stats().reference_push_failures == 2);

  // Output-only playback uses the same physical owner but no reference call,
  // so it cannot create an AEC epoch or inflate idle underflow diagnostics.
  speaker.max_accept = 2;
  assert(runtime.start_playback(16'000));
  assert(runtime.stats().playback_epoch != first_epoch);
  assert(runtime.write_playback(pcm) == 2);
  runtime.stop_playback();
  assert(frontend.begins == 1);
  assert(frontend.ends == 1);
  assert(runtime.stats().reference_epochs == 1);
  assert(runtime.stats().playback_starts == 2);
  assert(runtime.stats().playback_stops == 2);

  // Native 16-kHz reference is forwarded without conversion.
  assert(runtime.start_playback(16'000));
  assert(runtime.write_playback(direct16) == 2);
  assert(runtime.push_playback_reference(direct16, 16'000));
  assert(frontend.begins == 2);
  assert(frontend.last_reference_rate == 16'000);
  assert(runtime.stats().reference_converter_dropped_groups == 0);
  runtime.reset();
  assert(frontend.ends == 2);
  assert(frontend.resets == 2);
  assert(!runtime.stats().reference_active);
  assert(runtime.stats().reference_epochs == 2);
  runtime.stop_playback();

  // Unsupported source rates fail closed before opening an AEC epoch.
  assert(runtime.start_playback(22'050));
  assert(!runtime.push_playback_reference(direct16, 22'050));
  assert(runtime.stats().unsupported_reference_rates == 2);
  assert(runtime.stats().reference_push_failures == 3);
  assert(!runtime.stats().reference_active);
  runtime.stop_playback();

  // Restart with active capture + monitored playback cooperatively releases all
  // owned resources before frontend reinitialization.
  assert(runtime.start_capture());
  assert(runtime.start_playback(24'000));
  speaker.max_accept = 3;
  const size_t accepted_restart = runtime.write_playback(pcm);
  assert(accepted_restart == 3);
  assert(runtime.push_playback_reference(
      std::span<const int16_t>(pcm.data(), accepted_restart), 24'000));
  const size_t mic_stops_before_restart = microphone.stops;
  const size_t speaker_stops_before_restart = speaker.stops;
  assert(runtime.start());
  assert(microphone.stops == mic_stops_before_restart + 1);
  assert(speaker.stops == speaker_stops_before_restart + 1);
  assert(frontend.ends == 3);
  assert(frontend.resets == 3);
  assert(frontend.starts == 2);
  assert(!runtime.stats().capture_active);
  assert(!runtime.stats().playback_active);
  assert(!runtime.stats().reference_active);

  return 0;
}
