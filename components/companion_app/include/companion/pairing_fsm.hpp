#pragma once

#include <algorithm>
#include <array>
#include <cstddef>
#include <cstdint>
#include <string_view>

namespace companion::pairing {

enum class State : uint8_t {
  idle,
  discovering,
  session_pending,
  awaiting_confirmation,
  confirming,
};

enum class StopReason : uint8_t {
  none,
  success,
  cancelled,
  timed_out,
  disconnected,
  rejected,
  invalid_event,
  ambiguous,
};

struct Config {
  uint32_t discovery_window_ms{30'000};
  uint32_t confirmation_window_ms{30'000};
};

template <size_t N>
struct FixedText {
  std::array<char, N> bytes{};

  void clear() { bytes.fill('\0'); }
  bool set(std::string_view value) {
    if (value.empty() || value.size() >= bytes.size()) return false;
    clear();
    std::copy(value.begin(), value.end(), bytes.begin());
    return true;
  }
  std::string_view view() const {
    const auto end = std::find(bytes.begin(), bytes.end(), '\0');
    return {bytes.data(), static_cast<size_t>(end - bytes.begin())};
  }
};

// PairingFsm contains no radio/backend code. BLE discovery feeds rotating opaque
// aliases into observe_candidate(); the authenticated backend remains the only
// authority that turns an alias observation into a pairing session/relationship.
// Until #100 supplies calibrated proximity ranking, discovery is intentionally
// conservative: exactly one distinct peer may be committed automatically.
class PairingFsm final {
public:
  explicit constexpr PairingFsm(Config config = {}) : config_(config) {}

  State state() const { return state_; }
  StopReason last_stop_reason() const { return last_stop_reason_; }
  uint64_t deadline_ms() const { return deadline_ms_; }
  std::string_view local_alias() const { return local_alias_.view(); }
  std::string_view candidate_alias() const { return candidate_alias_.view(); }
  std::string_view proximity_evidence_id() const { return evidence_id_.view(); }
  std::string_view session_id() const { return session_id_.view(); }
  std::string_view confirmation_nonce() const { return confirmation_nonce_.view(); }
  bool ambiguous() const { return ambiguous_; }

  bool begin(uint64_t now_ms, std::string_view local_alias) {
    if (state_ != State::idle || config_.discovery_window_ms == 0 ||
        config_.discovery_window_ms > 60'000 || !valid_alias(local_alias)) return false;
    reset_fields();
    if (!local_alias_.set(local_alias)) return false;
    state_ = State::discovering;
    last_stop_reason_ = StopReason::none;
    deadline_ms_ = now_ms + config_.discovery_window_ms;
    return true;
  }

  // Repeated observations of one alias are harmless. A second distinct alias is
  // remembered only as ambiguity; it never replaces the first candidate and no
  // unbounded scan history is retained.
  bool observe_candidate(uint64_t now_ms, std::string_view candidate_alias,
                         std::string_view evidence_id) {
    if (!active_at(now_ms) || state_ != State::discovering ||
        !valid_alias(candidate_alias) || candidate_alias == local_alias_.view() ||
        evidence_id.empty() || evidence_id.size() > 128) {
      return false;
    }
    if (candidate_alias_.view().empty()) {
      return candidate_alias_.set(candidate_alias) && evidence_id_.set(evidence_id);
    }
    if (candidate_alias_.view() == candidate_alias) {
      return evidence_id_.set(evidence_id);
    }
    ambiguous_ = true;
    return false;
  }

  // Candidate selection is a separate explicit transition so callers can run a
  // bounded discovery interval before committing. Without #100 calibration, any
  // observed ambiguity fails closed instead of picking the strongest/first RSSI.
  bool commit_candidate(uint64_t now_ms) {
    if (!active_at(now_ms) || state_ != State::discovering || ambiguous_ ||
        candidate_alias_.view().empty() || evidence_id_.view().empty()) {
      if (ambiguous_) stop(StopReason::ambiguous);
      return false;
    }
    state_ = State::session_pending;
    return true;
  }

  // message_id from pairing.session_created is the participant-specific nonce.
  bool session_created(uint64_t now_ms, std::string_view session_id,
                       std::string_view confirmation_nonce,
                       uint64_t server_expiry_ms) {
    if (!active_at(now_ms) || state_ != State::session_pending ||
        session_id.empty() || session_id.size() > 128 ||
        confirmation_nonce.size() < 16 || confirmation_nonce.size() > 256 ||
        server_expiry_ms <= now_ms) {
      return fail_invalid();
    }
    if (!session_id_.set(session_id) || !confirmation_nonce_.set(confirmation_nonce)) return fail_invalid();
    state_ = State::awaiting_confirmation;
    const uint64_t local_expiry = now_ms + config_.confirmation_window_ms;
    deadline_ms_ = server_expiry_ms < local_expiry ? server_expiry_ms : local_expiry;
    return true;
  }

  bool confirm(uint64_t now_ms, std::string_view session_id) {
    if (!active_at(now_ms) || state_ != State::awaiting_confirmation || session_id != session_id_.view()) {
      return fail_invalid();
    }
    state_ = State::confirming;
    return true;
  }

  bool server_success(uint64_t now_ms, std::string_view session_id) {
    if (!active_at(now_ms) || state_ != State::confirming || session_id != session_id_.view()) return fail_invalid();
    stop(StopReason::success);
    return true;
  }

  bool server_rejected(std::string_view session_id) {
    if (state_ == State::idle || session_id.empty() || session_id != session_id_.view()) return false;
    stop(StopReason::rejected);
    return true;
  }

  bool server_expired(std::string_view session_id) {
    if (state_ == State::idle || session_id.empty() || session_id != session_id_.view()) return false;
    stop(StopReason::timed_out);
    return true;
  }

  void cancel() {
    if (state_ != State::idle) stop(StopReason::cancelled);
  }

  void disconnected() {
    if (state_ != State::idle) stop(StopReason::disconnected);
  }

  void tick(uint64_t now_ms) {
    if (state_ != State::idle && now_ms >= deadline_ms_) stop(StopReason::timed_out);
  }

private:
  static constexpr bool valid_alias(std::string_view value) {
    if (value.size() != 19 || !value.starts_with("CP-")) return false;
    for (const char c : value.substr(3)) {
      if ((c >= 'A' && c <= 'Z') || (c >= '2' && c <= '7')) continue;
      return false;
    }
    return true;
  }

  bool active_at(uint64_t now_ms) {
    if (state_ == State::idle) return false;
    if (now_ms >= deadline_ms_) {
      stop(StopReason::timed_out);
      return false;
    }
    return true;
  }

  bool fail_invalid() {
    if (state_ != State::idle) stop(StopReason::invalid_event);
    return false;
  }

  void stop(StopReason reason) {
    state_ = State::idle;
    last_stop_reason_ = reason;
    deadline_ms_ = 0;
    reset_fields();
  }

  void reset_fields() {
    local_alias_.clear();
    candidate_alias_.clear();
    evidence_id_.clear();
    session_id_.clear();
    confirmation_nonce_.clear();
    ambiguous_ = false;
  }

  Config config_{};
  State state_{State::idle};
  StopReason last_stop_reason_{StopReason::none};
  uint64_t deadline_ms_{};
  bool ambiguous_{};
  FixedText<20> local_alias_{};
  FixedText<20> candidate_alias_{};
  FixedText<129> evidence_id_{};
  FixedText<129> session_id_{};
  FixedText<257> confirmation_nonce_{};
};

} // namespace companion::pairing
