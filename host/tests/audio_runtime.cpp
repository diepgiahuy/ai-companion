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
  size_t begins{};
  size_t ends{};
  size_t resets{};
  std::vector<int16_t> references;
  PlaybackReferenceStats stats{};

  bool start() override { started = true; return true; }
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
  bool push_playback_reference(std::span<const int16_t> pcm) override {
    if (!active) return false;
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

  assert(runtime.start_capture());
  assert(runtime.start_capture());
  assert(microphone.starts == 1);
  std::array<int16_t, 4> capture{};
  assert(runtime.read_capture(capture) == 1);
  runtime.stop_capture();
  runtime.stop_capture();
  assert(microphone.stops == 1);

  // A playback epoch exists as soon as physical playback starts, but AEC
  // reference is not active until accepted TTS reference is actually supplied.
  assert(runtime.start_playback(24'000));
  const auto first_epoch = runtime.stats().playback_epoch;
  assert(first_epoch != 0);
  assert(!runtime.stats().reference_active);

  const std::array<int16_t, 4> pcm{10, 20, 30, 40};
  assert(runtime.write_playback(pcm) == 2);
  assert(runtime.stats().accepted_playback_samples == 2);
  assert(frontend.begins == 0);

  const std::array<int16_t, 2> reference{10, 25};
  assert(runtime.push_playback_reference(reference));
  assert(frontend.begins == 1);
  assert(runtime.stats().reference_active);
  assert(runtime.stats().reference_epochs == 1);
  assert((frontend.references == std::vector<int16_t>{10, 25}));

  runtime.stop_playback();
  assert(frontend.ends == 1);
  assert(!runtime.stats().reference_active);
  assert(runtime.stats().playback_stops == 1);
  assert(!runtime.push_playback_reference(reference));
  assert(runtime.stats().reference_push_failures == 1);

  // Voice-mail/alarm-style playback uses the same physical owner but, because
  // no AEC reference is supplied, it cannot accidentally create a monitored
  // reference epoch or inflate idle underflow diagnostics.
  assert(runtime.start_playback(16'000));
  assert(runtime.stats().playback_epoch != first_epoch);
  assert(runtime.write_playback(pcm) == 2);
  runtime.stop_playback();
  assert(frontend.begins == 1);
  assert(frontend.ends == 1);
  assert(runtime.stats().reference_epochs == 1);
  assert(runtime.stats().playback_starts == 2);
  assert(runtime.stats().playback_stops == 2);

  // Frontend reset is an epoch boundary but does not erase lifetime metrics.
  assert(runtime.start_playback(24'000));
  assert(runtime.write_playback(pcm) == 2);
  assert(runtime.push_playback_reference(reference));
  assert(frontend.begins == 2);
  runtime.reset();
  assert(frontend.ends == 2);
  assert(frontend.resets == 1);
  assert(!runtime.stats().reference_active);
  assert(runtime.stats().reference_epochs == 2);
  runtime.stop_playback();

  return 0;
}
