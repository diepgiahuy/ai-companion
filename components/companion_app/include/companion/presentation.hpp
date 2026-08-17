#pragma once

#include <algorithm>
#include <array>
#include <cstddef>
#include <cstdint>
#include <string_view>

namespace companion {

enum class PresentationActivity : uint8_t {
  booting,
  connecting,
  ready,
  idle,
  listening,
  thinking,
  tool,
  speaking,
  interrupted,
  offline,
  reconnecting,
  error,
};

enum class PresentationDomain : uint8_t {
  confirmation,
  privacy,
  pairing,
  ota,
  alarm,
  reminder,
  voice_mail,
  degraded,
  card,
  count,
};

enum class PresentationScope : uint8_t {
  global,
  session,
  generation,
};

enum class PresentationPriority : uint8_t {
  p0_security = 0,
  p1_blocking = 1,
  p2_alarm = 2,
  p3_attention = 3,
  p4_degraded = 4,
  p5_voice = 5,
  p6_card = 6,
  p7_idle = 7,
};

struct PresentationCounters {
  uint32_t stale_drops{};
  uint32_t duplicate_drops{};
  uint32_t revision_conflicts{};
  uint32_t coalesced_updates{};
  uint32_t truncated_text{};
};

struct PresentationEvent {
  enum class Kind : uint8_t { base, attention, clear_attention };

  Kind kind{Kind::base};
  PresentationActivity activity{PresentationActivity::booting};
  PresentationDomain domain{PresentationDomain::card};
  PresentationScope scope{PresentationScope::global};
  uint64_t session_epoch{};
  uint64_t generation{};
  uint64_t revision{};
  std::string_view text{};
};

struct PresentationModel {
  enum class Surface : uint8_t { base, attention };

  Surface surface{Surface::base};
  PresentationActivity base_activity{PresentationActivity::booting};
  PresentationActivity activity{PresentationActivity::booting};
  PresentationDomain domain{PresentationDomain::card};
  PresentationPriority priority{PresentationPriority::p7_idle};
  PresentationScope scope{PresentationScope::global};
  uint64_t session_epoch{};
  uint64_t generation{};
  uint64_t revision{};
  std::array<char, 97> text{};

  std::string_view text_view() const {
    const auto end = std::find(text.begin(), text.end(), '\0');
    return {text.data(), static_cast<size_t>(end - text.begin())};
  }
};

class PresentationReducer final {
public:
  PresentationReducer() { reset(); }

  void reset(uint64_t session_epoch = 0, uint64_t generation = 0) {
    session_epoch_ = session_epoch;
    generation_ = generation;
    base_ = {};
    base_.active = true;
    base_.activity = PresentationActivity::booting;
    base_.priority = priority_for(PresentationActivity::booting);
    for (auto& slot : attention_) slot = {};
    counters_ = {};
  }

  void set_context(uint64_t session_epoch, uint64_t generation) {
    if (session_epoch != session_epoch_) {
      session_epoch_ = session_epoch;
      generation_ = generation;
      invalidate_scope(PresentationScope::session);
      invalidate_scope(PresentationScope::generation);
      return;
    }
    if (generation != generation_) {
      generation_ = generation;
      invalidate_scope(PresentationScope::generation);
    }
  }

  bool apply(const PresentationEvent& event) {
    if (!scope_current(event.scope, event.session_epoch, event.generation)) {
      ++counters_.stale_drops;
      return false;
    }
    switch (event.kind) {
    case PresentationEvent::Kind::base:
      return apply_base(event);
    case PresentationEvent::Kind::attention:
      return apply_attention(event);
    case PresentationEvent::Kind::clear_attention:
      return clear_attention(event);
    }
    return false;
  }

  PresentationModel model() const {
    const Candidate* winner = &base_;
    PresentationDomain winner_domain = PresentationDomain::card;
    bool winner_is_attention = false;

    for (size_t index = 0; index < attention_.size(); ++index) {
      const Candidate& candidate = attention_[index];
      if (!candidate.active) continue;
      const auto domain = static_cast<PresentationDomain>(index);
      const auto candidate_priority = static_cast<uint8_t>(candidate.priority);
      const auto winner_priority = static_cast<uint8_t>(winner->priority);
      if (candidate_priority < winner_priority ||
          (candidate_priority == winner_priority &&
           (!winner_is_attention || domain_order(domain) < domain_order(winner_domain)))) {
        winner = &candidate;
        winner_domain = domain;
        winner_is_attention = true;
      }
    }

    PresentationModel out{};
    out.surface = winner_is_attention ? PresentationModel::Surface::attention
                                      : PresentationModel::Surface::base;
    out.base_activity = base_.activity;
    out.activity = winner->activity;
    out.domain = winner_domain;
    out.priority = winner->priority;
    out.scope = winner->scope;
    out.session_epoch = winner->session_epoch;
    out.generation = winner->generation;
    out.revision = winner->revision;
    out.text = winner->text;
    return out;
  }

  const PresentationCounters& counters() const { return counters_; }
  uint64_t session_epoch() const { return session_epoch_; }
  uint64_t generation() const { return generation_; }

  static constexpr PresentationPriority priority_for(PresentationDomain domain) {
    switch (domain) {
    case PresentationDomain::confirmation:
    case PresentationDomain::privacy:
      return PresentationPriority::p0_security;
    case PresentationDomain::pairing:
    case PresentationDomain::ota:
      return PresentationPriority::p1_blocking;
    case PresentationDomain::alarm:
      return PresentationPriority::p2_alarm;
    case PresentationDomain::reminder:
    case PresentationDomain::voice_mail:
      return PresentationPriority::p3_attention;
    case PresentationDomain::degraded:
      return PresentationPriority::p4_degraded;
    case PresentationDomain::card:
    case PresentationDomain::count:
      return PresentationPriority::p6_card;
    }
    return PresentationPriority::p6_card;
  }

  static constexpr PresentationPriority priority_for(PresentationActivity activity) {
    switch (activity) {
    case PresentationActivity::connecting:
    case PresentationActivity::offline:
    case PresentationActivity::reconnecting:
    case PresentationActivity::error:
      return PresentationPriority::p4_degraded;
    case PresentationActivity::listening:
    case PresentationActivity::thinking:
    case PresentationActivity::tool:
    case PresentationActivity::speaking:
    case PresentationActivity::interrupted:
      return PresentationPriority::p5_voice;
    case PresentationActivity::booting:
    case PresentationActivity::ready:
    case PresentationActivity::idle:
      return PresentationPriority::p7_idle;
    }
    return PresentationPriority::p7_idle;
  }

private:
  struct Candidate {
    bool active{};
    bool revision_seen{};
    PresentationActivity activity{PresentationActivity::booting};
    PresentationPriority priority{PresentationPriority::p7_idle};
    PresentationScope scope{PresentationScope::global};
    uint64_t session_epoch{};
    uint64_t generation{};
    uint64_t revision{};
    std::array<char, 97> text{};
  };

  static constexpr size_t domain_index(PresentationDomain domain) {
    return static_cast<size_t>(domain);
  }

  static constexpr uint8_t domain_order(PresentationDomain domain) {
    switch (domain) {
    case PresentationDomain::privacy:
      return 0;
    case PresentationDomain::confirmation:
      return 1;
    case PresentationDomain::ota:
      return 2;
    case PresentationDomain::pairing:
      return 3;
    case PresentationDomain::alarm:
      return 4;
    case PresentationDomain::reminder:
      return 5;
    case PresentationDomain::voice_mail:
      return 6;
    case PresentationDomain::degraded:
      return 7;
    case PresentationDomain::card:
      return 8;
    case PresentationDomain::count:
      return 9;
    }
    return 9;
  }

  bool scope_current(PresentationScope scope, uint64_t session_epoch,
                     uint64_t generation) const {
    switch (scope) {
    case PresentationScope::global:
      return true;
    case PresentationScope::session:
      return session_epoch == session_epoch_;
    case PresentationScope::generation:
      return session_epoch == session_epoch_ && generation == generation_;
    }
    return false;
  }

  bool apply_base(const PresentationEvent& event) {
    if (!accept_revision(base_, event.revision, event.text)) return false;
    base_.active = true;
    base_.revision_seen = true;
    base_.activity = event.activity;
    base_.priority = priority_for(event.activity);
    base_.scope = event.scope;
    base_.session_epoch = event.session_epoch;
    base_.generation = event.generation;
    base_.revision = event.revision;
    set_text(base_.text, event.text);
    return true;
  }

  bool apply_attention(const PresentationEvent& event) {
    if (event.domain == PresentationDomain::count) return false;
    Candidate& slot = attention_[domain_index(event.domain)];
    if (!accept_revision(slot, event.revision, event.text)) return false;
    if (slot.active) ++counters_.coalesced_updates;
    slot.active = true;
    slot.revision_seen = true;
    slot.activity = base_.activity;
    slot.priority = priority_for(event.domain);
    slot.scope = event.scope;
    slot.session_epoch = event.session_epoch;
    slot.generation = event.generation;
    slot.revision = event.revision;
    set_text(slot.text, event.text);
    return true;
  }

  bool clear_attention(const PresentationEvent& event) {
    if (event.domain == PresentationDomain::count) return false;
    Candidate& slot = attention_[domain_index(event.domain)];
    if (!slot.active) {
      ++counters_.duplicate_drops;
      return false;
    }
    if (slot.revision_seen && event.revision < slot.revision) {
      ++counters_.stale_drops;
      return false;
    }
    slot = {};
    return true;
  }

  bool accept_revision(const Candidate& current, uint64_t revision,
                       std::string_view text) {
    if (!current.revision_seen) return true;
    if (revision < current.revision) {
      ++counters_.stale_drops;
      return false;
    }
    if (revision == current.revision) {
      if (text_view(current.text) == text) {
        ++counters_.duplicate_drops;
      } else {
        ++counters_.revision_conflicts;
      }
      return false;
    }
    return true;
  }

  void invalidate_scope(PresentationScope scope) {
    if (base_.active && base_.scope == scope) {
      base_ = {};
      base_.active = true;
      base_.activity = PresentationActivity::booting;
      base_.priority = priority_for(PresentationActivity::booting);
    }
    for (auto& slot : attention_) {
      if (slot.active && slot.scope == scope) slot = {};
    }
  }

  void set_text(std::array<char, 97>& destination, std::string_view source) {
    destination.fill('\0');
    const size_t count = std::min(source.size(), destination.size() - 1);
    std::copy_n(source.begin(), count, destination.begin());
    if (source.size() > count) ++counters_.truncated_text;
  }

  static std::string_view text_view(const std::array<char, 97>& text) {
    const auto end = std::find(text.begin(), text.end(), '\0');
    return {text.data(), static_cast<size_t>(end - text.begin())};
  }

  uint64_t session_epoch_{};
  uint64_t generation_{};
  Candidate base_{};
  std::array<Candidate, static_cast<size_t>(PresentationDomain::count)> attention_{};
  PresentationCounters counters_{};
};

} // namespace companion
