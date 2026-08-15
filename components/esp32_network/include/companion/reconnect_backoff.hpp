#pragma once

#include <algorithm>
#include <cstdint>

namespace companion {

class ReconnectBackoff final {
public:
  static constexpr uint32_t kInitialDelayMs = 500;
  static constexpr uint32_t kMaximumDelayMs = 30'000;
  static constexpr uint32_t kJitterWindowMs = 250;

  uint32_t next_delay_ms(uint32_t random_value) {
    const uint32_t shift = std::min<uint32_t>(attempt_, 6);
    const uint32_t exponential = kInitialDelayMs << shift;
    const uint32_t base = std::min(exponential, kMaximumDelayMs);
    const uint32_t jitter = random_value % (kJitterWindowMs + 1);
    ++attempt_;
    return std::min<uint32_t>(base + jitter, kMaximumDelayMs);
  }

  void reset() { attempt_ = 0; }
  uint32_t attempt() const { return attempt_; }

private:
  uint32_t attempt_{};
};

} // namespace companion
