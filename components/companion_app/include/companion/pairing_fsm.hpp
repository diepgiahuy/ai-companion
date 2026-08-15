#pragma once

#include <array>
#include <cstddef>
#include <cstdint>
#include <cstring>
#include <string_view>

namespace companion::pairing {

constexpr size_t kMaximumObservations = 8;
constexpr uint64_t kDefaultDiscoveryWindowMs = 60'000;
constexpr size_t kDiscoveryIDCapacity = 20; // "CP-" + 16 base32 symbols + NUL.
constexpr size_t kOpaqueSessionCapacity = 96;

inline bool valid_discovery_id(std::string_view value) {
  if (value.size() != 19 || !value.starts_with("CP-")) return false;
  for (const char c : value.substr(3)) {
    if ((c >= 'A' && c <= 'Z') || (c >= '2' && c <= '7')) continue;
    return false;
  }
  return true;
}

enum class State : uint8_t {
  idle,
  discovering,
  awaiting_session,
  awaiting_confirmation,
  confirming,
  succeeded,
  rejected,
  expired,
  disconnected,
};

struct Observation {
  std::array<char, kDiscoveryIDCapacity> discovery_id{};
  int8_t rssi{};
  uint64_t last_seen_ms{};

  std::string_view id() const {
    return {discovery_id.data(), std::strlen(discovery_id.data())};
  }
};

class Fsm final {
public:
  bool start(uint64_t now_ms, uint64_t discovery_window_ms = kDefaultDiscoveryWindowMs) {
    if (state_ != State::idle && terminal() == false) return false;
    clear();
    state_ = State::discovering;
    discovery_deadline_ms_ = now_ms + discovery_window_ms;
    return true;
  }

  bool observe(std::string_view discovery_id, int8_t rssi, uint64_t now_ms) {
    if (state_ != State::discovering || !valid_discovery_id(discovery_id) || now_ms >= discovery_deadline_ms_) {
      return false;
    }
    for (size_t index = 0; index < observation_count_; ++index) {
      if (observations_[index].id() == discovery_id) {
        observations_[index].rssi = rssi;
        observations_[index].last_seen_ms = now_ms;
        return true;
      }
    }
    if (observation_count_ >= observations_.size()) {
      // Bounded memory is an invariant. Do not evict one peer and silently
      // reinterpret an ambiguous RF environment as a unique candidate.
      overflowed_ = true;
      return false;
    }
    auto& target = observations_[observation_count_++];
    std::memcpy(target.discovery_id.data(), discovery_id.data(), discovery_id.size());
    target.discovery_id[discovery_id.size()] = '\0';
    target.rssi = rssi;
    target.last_seen_ms = now_ms;
    return true;
  }

  // #100 owns calibrated RSSI thresholds/ranking. Until then, firmware only
  // auto-selects when the discovery set is unambiguous: exactly one opaque peer.
  std::string_view unique_candidate() const {
    if (state_ != State::discovering || overflowed_ || observation_count_ != 1) return {};
    return observations_[0].id();
  }

  bool request_session(uint64_t now_ms) {
    if (now_ms >= discovery_deadline_ms_ || unique_candidate().empty()) return false;
    state_ = State::awaiting_session;
    return true;
  }

  bool session_created(std::string_view session_id, std::string_view confirmation_nonce,
                       uint64_t expires_at_ms, uint64_t now_ms) {
    if ((state_ != State::awaiting_session && state_ != State::discovering) ||
        session_id.empty() || confirmation_nonce.empty() || expires_at_ms <= now_ms ||
        session_id.size() >= session_id_.size() || confirmation_nonce.size() >= confirmation_nonce_.size()) {
      return false;
    }
    copy(session_id_, session_id);
    copy(confirmation_nonce_, confirmation_nonce);
    session_expires_at_ms_ = expires_at_ms;
    state_ = State::awaiting_confirmation;
    return true;
  }

  bool confirm(uint64_t now_ms) {
    if (state_ != State::awaiting_confirmation || now_ms >= session_expires_at_ms_) return false;
    state_ = State::confirming;
    return true;
  }

  bool succeeded(std::string_view session_id, uint64_t now_ms) {
    if ((state_ != State::confirming && state_ != State::awaiting_confirmation) ||
        now_ms >= session_expires_at_ms_ || session_id != active_session_id()) return false;
    state_ = State::succeeded;
    return true;
  }

  bool rejected(std::string_view session_id) {
    if (!session_matches(session_id)) return false;
    state_ = State::rejected;
    return true;
  }

  bool expired(std::string_view session_id) {
    if (!session_matches(session_id)) return false;
    state_ = State::expired;
    return true;
  }

  void disconnected() {
    if (state_ != State::idle && !terminal()) state_ = State::disconnected;
  }

  void cancel() {
    if (state_ != State::idle && !terminal()) state_ = State::rejected;
  }

  void tick(uint64_t now_ms) {
    if (state_ == State::discovering && now_ms >= discovery_deadline_ms_) {
      state_ = State::expired;
    } else if ((state_ == State::awaiting_session || state_ == State::awaiting_confirmation || state_ == State::confirming) &&
               session_expires_at_ms_ != 0 && now_ms >= session_expires_at_ms_) {
      state_ = State::expired;
    }
  }

  void reset() { clear(); }

  State state() const { return state_; }
  bool terminal() const {
    return state_ == State::succeeded || state_ == State::rejected ||
           state_ == State::expired || state_ == State::disconnected;
  }
  bool overflowed() const { return overflowed_; }
  size_t observation_count() const { return observation_count_; }
  std::string_view active_session_id() const { return view(session_id_); }
  std::string_view confirmation_nonce() const { return view(confirmation_nonce_); }

private:
  template <size_t N>
  static void copy(std::array<char, N>& target, std::string_view value) {
    target.fill('\0');
    std::memcpy(target.data(), value.data(), value.size());
  }
  template <size_t N>
  static std::string_view view(const std::array<char, N>& value) {
    return {value.data(), std::strlen(value.data())};
  }
  bool session_matches(std::string_view session_id) const {
    return !session_id.empty() && session_id == active_session_id() &&
           (state_ == State::awaiting_confirmation || state_ == State::confirming || state_ == State::awaiting_session);
  }
  void clear() {
    state_ = State::idle;
    observations_ = {};
    observation_count_ = 0;
    overflowed_ = false;
    discovery_deadline_ms_ = 0;
    session_expires_at_ms_ = 0;
    session_id_.fill('\0');
    confirmation_nonce_.fill('\0');
  }

  State state_{State::idle};
  std::array<Observation, kMaximumObservations> observations_{};
  size_t observation_count_{};
  bool overflowed_{};
  uint64_t discovery_deadline_ms_{};
  uint64_t session_expires_at_ms_{};
  std::array<char, kOpaqueSessionCapacity> session_id_{};
  std::array<char, kOpaqueSessionCapacity> confirmation_nonce_{};
};

} // namespace companion::pairing
