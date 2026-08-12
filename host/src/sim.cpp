#include "companion/app.hpp"
#include "companion/mock_backend.hpp"

#include <algorithm>
#include <iostream>
#include <span>
#include <string>
#include <utility>

using namespace companion;

namespace {
struct SimMicrophone final : Microphone {
  bool active{};
  bool start_capture() override { active = true; return true; }
  size_t read_capture(std::span<int16_t> output) override {
    if (!active) return 0;
    const size_t count = std::min(output.size(), kAudioFrameSamples);
    std::fill_n(output.begin(), count, 500);
    return count;
  }
  void stop_capture() override { active = false; }
};
struct SimSpeaker final : Speaker {
  bool active{};
  size_t total{};
  bool start_playback(uint32_t rate) override {
    active = rate == kAudioSampleRateHz;
    total = 0;
    std::cout << "[speaker] start " << rate << " Hz\n";
    return active;
  }
  size_t write_playback(std::span<const int16_t> pcm) override {
    if (!active) return 0;
    total += pcm.size();
    return pcm.size();
  }
  bool playback_drained() const override { return true; }
  void stop_playback() override {
    if (active) std::cout << "[speaker] stop after " << total << " samples\n";
    active = false;
  }
};
struct SimDisplay final : Display {
  void show(UiState state, std::string_view text) override {
    std::cout << "[display " << static_cast<int>(state) << "] " << text << '\n';
  }
};
struct SimButton final : Button {
  bool pending{};
  bool consume_press(uint64_t) override { return std::exchange(pending, false); }
};
}

int main() {
  SimMicrophone microphone;
  SimSpeaker speaker;
  SimDisplay display;
  SimButton button;
  MockVoiceBackend backend;
  CompanionApp app(microphone, speaker, display, button, backend);
  uint64_t now = 0;
  app.start(now);
  app.tick(now);

  std::cout << "Commands: press | tick <milliseconds> | quit\n";
  std::string command;
  while (std::cin >> command && command != "quit") {
    if (command == "press") button.pending = true;
    if (command == "tick") {
      uint64_t delta{};
      std::cin >> delta;
      now += delta;
    } else {
      now += 20;
    }
    app.tick(now);
    std::cout << "[samples] " << app.streamed_samples() << '\n';
  }
}
