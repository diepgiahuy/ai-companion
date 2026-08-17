#pragma once

#include "companion/app.hpp"
#include "companion/presentation.hpp"

#include <algorithm>
#include <array>
#include <cstddef>
#include <cstdint>
#include <string_view>

namespace companion {

// Migration adapter for #228. Existing runtime code still emits legacy
// Display::show(UiState, text) calls, while this adapter gives the local
// renderer one deterministic PresentationReducer-owned surface. It must be
// deleted once runtime/domain owners emit PresentationEvent directly.
class PresentationDisplay final : public Display {
public:
  explicit PresentationDisplay(Display& sink) : sink_(sink) {
    attention_state_.fill(UiState::ready);
  }

  void show(UiState state, std::string_view text) override {
    PresentationDomain legacy_domain{};
    if (legacy_attention_domain(state, legacy_domain)) {
      if (legacy_attention_active_ && legacy_attention_domain_ != legacy_domain) {
        (void)clear_attention_internal(legacy_attention_domain_, false);
      }
      legacy_attention_active_ = true;
      legacy_attention_domain_ = legacy_domain;
      (void)show_attention_internal(legacy_domain, state, text,
                                    PresentationScope::global, 0, 0, true);
      return;
    }

    if (legacy_attention_active_) {
      (void)clear_attention_internal(legacy_attention_domain_, false);
      legacy_attention_active_ = false;
    }
    if (transient_active_) {
      (void)clear_attention_internal(PresentationDomain::card, false);
      transient_active_ = false;
    }

    base_state_ = state;
    const PresentationEvent event{
        .kind = PresentationEvent::Kind::base,
        .activity = activity_for(state),
        .scope = PresentationScope::global,
        .revision = next_base_revision_++,
        .text = text,
    };
    (void)reducer_.apply(event);
    render_current();
  }

  bool show_attention(PresentationDomain domain, UiState renderer_state,
                      std::string_view text,
                      PresentationScope scope = PresentationScope::global,
                      uint64_t session_epoch = 0, uint64_t generation = 0) {
    return show_attention_internal(domain, renderer_state, text, scope,
                                   session_epoch, generation, true);
  }

  bool clear_attention(PresentationDomain domain,
                       PresentationScope scope = PresentationScope::global,
                       uint64_t session_epoch = 0, uint64_t generation = 0) {
    return clear_attention_internal(domain, true, scope, session_epoch, generation);
  }

  // Pairing completion/failure is informational rather than a blocking pairing
  // state. Keep it as a low-priority semantic card until the next base render,
  // which matches the legacy "last status until normal UI updates" behavior.
  bool show_transient(UiState renderer_state, std::string_view text) {
    const bool applied = show_attention_internal(PresentationDomain::card,
                                                 renderer_state, text,
                                                 PresentationScope::global,
                                                 0, 0, true);
    if (applied) transient_active_ = true;
    return applied;
  }

  void set_context(uint64_t session_epoch, uint64_t generation) {
    reducer_.set_context(session_epoch, generation);
    render_current();
  }

  PresentationModel model() const { return reducer_.model(); }
  const PresentationCounters& counters() const { return reducer_.counters(); }
  uint32_t rendered_frames() const { return rendered_frames_; }

private:
  static constexpr size_t domain_index(PresentationDomain domain) {
    return static_cast<size_t>(domain);
  }

  static constexpr PresentationActivity activity_for(UiState state) {
    switch (state) {
    case UiState::booting:
      return PresentationActivity::booting;
    case UiState::connecting:
      return PresentationActivity::connecting;
    case UiState::ready:
      return PresentationActivity::ready;
    case UiState::idle:
      return PresentationActivity::idle;
    case UiState::listening:
      return PresentationActivity::listening;
    case UiState::processing:
      return PresentationActivity::thinking;
    case UiState::speaking:
      return PresentationActivity::speaking;
    case UiState::error:
      return PresentationActivity::error;
    case UiState::voice_mail_waiting:
    case UiState::voice_mail_claiming:
    case UiState::voice_mail_playing:
    case UiState::alarm:
      break;
    }
    return PresentationActivity::ready;
  }

  static constexpr bool legacy_attention_domain(UiState state,
                                                 PresentationDomain& domain) {
    switch (state) {
    case UiState::alarm:
      domain = PresentationDomain::alarm;
      return true;
    case UiState::voice_mail_waiting:
    case UiState::voice_mail_claiming:
    case UiState::voice_mail_playing:
      domain = PresentationDomain::voice_mail;
      return true;
    case UiState::booting:
    case UiState::connecting:
    case UiState::ready:
    case UiState::idle:
    case UiState::listening:
    case UiState::processing:
    case UiState::speaking:
    case UiState::error:
      return false;
    }
    return false;
  }

  static std::string_view text_view(const std::array<char, 97>& text) {
    const auto end = std::find(text.begin(), text.end(), '\0');
    return {text.data(), static_cast<size_t>(end - text.begin())};
  }

  bool show_attention_internal(PresentationDomain domain, UiState renderer_state,
                               std::string_view text, PresentationScope scope,
                               uint64_t session_epoch, uint64_t generation,
                               bool render) {
    if (domain == PresentationDomain::count) return false;
    const size_t index = domain_index(domain);
    attention_state_[index] = renderer_state;
    const PresentationEvent event{
        .kind = PresentationEvent::Kind::attention,
        .domain = domain,
        .scope = scope,
        .session_epoch = session_epoch,
        .generation = generation,
        .revision = next_attention_revision_[index]++,
        .text = text,
    };
    const bool applied = reducer_.apply(event);
    if (applied) {
      attention_active_[index] = true;
      if (render) render_current();
    }
    return applied;
  }

  bool clear_attention_internal(PresentationDomain domain, bool render,
                                PresentationScope scope = PresentationScope::global,
                                uint64_t session_epoch = 0,
                                uint64_t generation = 0) {
    if (domain == PresentationDomain::count) return false;
    const size_t index = domain_index(domain);
    if (!attention_active_[index]) return false;
    const PresentationEvent event{
        .kind = PresentationEvent::Kind::clear_attention,
        .domain = domain,
        .scope = scope,
        .session_epoch = session_epoch,
        .generation = generation,
        .revision = next_attention_revision_[index]++,
    };
    const bool applied = reducer_.apply(event);
    if (applied) {
      attention_active_[index] = false;
      if (render) render_current();
    }
    return applied;
  }

  void render_current() {
    const PresentationModel current = reducer_.model();
    UiState renderer_state = base_state_;
    if (current.surface == PresentationModel::Surface::attention &&
        current.domain != PresentationDomain::count) {
      renderer_state = attention_state_[domain_index(current.domain)];
    }

    if (rendered_ && renderer_state == rendered_state_ &&
        current.text_view() == text_view(rendered_text_)) {
      return;
    }

    sink_.show(renderer_state, current.text_view());
    rendered_ = true;
    rendered_state_ = renderer_state;
    rendered_text_ = current.text;
    ++rendered_frames_;
  }

  Display& sink_;
  PresentationReducer reducer_{};
  UiState base_state_{UiState::booting};
  std::array<UiState, static_cast<size_t>(PresentationDomain::count)> attention_state_{};
  std::array<bool, static_cast<size_t>(PresentationDomain::count)> attention_active_{};
  std::array<uint64_t, static_cast<size_t>(PresentationDomain::count)> next_attention_revision_{};
  uint64_t next_base_revision_{};
  bool legacy_attention_active_{};
  PresentationDomain legacy_attention_domain_{PresentationDomain::card};
  bool transient_active_{};
  bool rendered_{};
  UiState rendered_state_{UiState::booting};
  std::array<char, 97> rendered_text_{};
  uint32_t rendered_frames_{};
};

} // namespace companion
