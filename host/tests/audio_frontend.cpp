#include "companion/audio_frontend.hpp"

#include <array>
#include <cassert>
#include <cstdint>

using companion::PlaybackReference24To16;

int main() {
  PlaybackReference24To16 converter;

  {
    const std::array<int16_t, 6> input{0, 10, 20, 30, 40, 50};
    std::array<int16_t, 4> output{};
    const size_t written = converter.convert(input, output);
    assert(written == 4);
    assert(output[0] == 0);
    assert(output[1] == 15);
    assert(output[2] == 30);
    assert(output[3] == 45);
    assert(converter.pending_samples() == 0);
    assert(converter.dropped_groups() == 0);
  }

  converter.reset();
  {
    const std::array<int16_t, 2> first{100, 200};
    std::array<int16_t, 4> output{};
    assert(converter.convert(first, output) == 0);
    assert(converter.pending_samples() == 2);

    const std::array<int16_t, 4> second{300, 400, 500, 600};
    const size_t written = converter.convert(second, output);
    assert(written == 4);
    assert(output[0] == 100);
    assert(output[1] == 250);
    assert(output[2] == 400);
    assert(output[3] == 550);
    assert(converter.pending_samples() == 0);
  }

  converter.reset();
  {
    // A saturated consumer cannot make buffering unbounded. A complete group
    // is dropped and accounted for; no stale reference is emitted later.
    const std::array<int16_t, 3> input{1, 2, 3};
    std::array<int16_t, 1> output{};
    assert(converter.convert(input, output) == 0);
    assert(converter.pending_samples() == 0);
    assert(converter.dropped_groups() == 1);
  }

  // Epoch/phase reset must not erase soak diagnostics.
  converter.reset();
  assert(converter.pending_samples() == 0);
  assert(converter.dropped_groups() == 1);
  converter.clear_metrics();
  assert(converter.dropped_groups() == 0);

  return 0;
}
