#include "companion/presentation_display.hpp"

#include <array>
#include <cassert>
#include <string_view>

using namespace companion;

namespace {
class RecordingDisplay final : public Display {
public:
  void show(UiState state, std::string_view text) override {
    state_ = state;
    text_.fill('\0');
    const size_t count = std::min(text.size(), text_.size() - 1);
    std::copy_n(text.begin(), count, text_.begin());
    ++calls_;
  }

  UiState state() const { return state_; }
  std::string_view text() const {
    const auto end = std::find(text_.begin(), text_.end(), '\0');
    return {text_.data(), static_cast<size_t>(end - text_.begin())};
  }
  uint32_t calls() const { return calls_; }

private:
  UiState state_{UiState::booting};
  std::array<char, 97> text_{};
  uint32_t calls_{};
};
} // namespace

int main() {
  {
    RecordingDisplay sink;
    PresentationDisplay display(sink);

    display.show(UiState::speaking, "SPEAKING");
    assert(sink.state() == UiState::speaking);
    assert(sink.text() == "SPEAKING");

    assert(display.show_attention(PresentationDomain::pairing, UiState::ready,
                                  "PAIR SEARCH"));
    assert(sink.state() == UiState::ready);
    assert(sink.text() == "PAIR SEARCH");

    assert(display.show_attention(PresentationDomain::confirmation, UiState::ready,
                                  "CONFIRM DELETE"));
    assert(sink.text() == "CONFIRM DELETE");

    // A base update remains live under the P0 overlay but does not redraw the
    // physical sink while the visible overlay itself is unchanged.
    const uint32_t before = sink.calls();
    display.show(UiState::error, "DISCONNECTED");
    assert(display.model().activity == PresentationActivity::error);
    assert(display.model().domain == PresentationDomain::confirmation);
    assert(sink.calls() == before);
    assert(sink.text() == "CONFIRM DELETE");

    assert(display.clear_attention(PresentationDomain::confirmation));
    assert(sink.text() == "PAIR SEARCH");
    assert(display.clear_attention(PresentationDomain::pairing));
    assert(sink.state() == UiState::error);
    assert(sink.text() == "DISCONNECTED");
  }

  {
    RecordingDisplay sink;
    PresentationDisplay display(sink);
    display.show(UiState::ready, "READY");

    display.show(UiState::alarm, "ALARM");
    assert(display.model().domain == PresentationDomain::alarm);
    assert(sink.state() == UiState::alarm);

    // Clearing legacy attention and updating the base are one semantic
    // transition and therefore one renderer write, not a clear-frame + redraw.
    const uint32_t before = sink.calls();
    display.show(UiState::listening, "LISTENING");
    assert(display.model().surface == PresentationModel::Surface::base);
    assert(sink.state() == UiState::listening);
    assert(sink.text() == "LISTENING");
    assert(sink.calls() == before + 1);
  }

  {
    RecordingDisplay sink;
    PresentationDisplay display(sink);
    display.show(UiState::ready, "READY");

    display.show(UiState::voice_mail_waiting, "VOICE MAIL");
    assert(sink.state() == UiState::voice_mail_waiting);
    display.show(UiState::voice_mail_claiming, "OPENING");
    assert(sink.state() == UiState::voice_mail_claiming);
    display.show(UiState::voice_mail_playing, "PLAYING");
    assert(sink.state() == UiState::voice_mail_playing);
    assert(display.model().domain == PresentationDomain::voice_mail);

    const uint32_t before = sink.calls();
    display.show(UiState::ready, "READY AGAIN");
    assert(display.model().surface == PresentationModel::Surface::base);
    assert(sink.state() == UiState::ready);
    assert(sink.text() == "READY AGAIN");
    assert(sink.calls() == before + 1);
  }

  {
    RecordingDisplay sink;
    PresentationDisplay display(sink);
    display.show(UiState::ready, "READY");
    const uint32_t after_ready = sink.calls();

    // Repeated equivalent visible output is reduced but not redrawn over I2C.
    display.show(UiState::ready, "READY");
    assert(sink.calls() == after_ready);

    assert(display.show_attention(PresentationDomain::confirmation, UiState::ready,
                                  "CONFIRM"));
    const uint32_t after_confirmation = sink.calls();
    assert(display.show_attention(PresentationDomain::pairing, UiState::ready,
                                  "PAIR HIDDEN"));
    assert(sink.calls() == after_confirmation);
    assert(sink.text() == "CONFIRM");
  }

  {
    RecordingDisplay sink;
    PresentationDisplay display(sink);
    display.show(UiState::ready, "READY");
    assert(display.show_transient(UiState::error, "PAIR ENDED"));
    assert(display.model().domain == PresentationDomain::card);
    assert(sink.state() == UiState::error);
    assert(sink.text() == "PAIR ENDED");

    // Retiring the transient and updating the next base snapshot is also one
    // renderer write, preserving the legacy status-until-next-update behavior.
    const uint32_t before = sink.calls();
    display.show(UiState::idle, "IDLE");
    assert(display.model().surface == PresentationModel::Surface::base);
    assert(sink.state() == UiState::idle);
    assert(sink.text() == "IDLE");
    assert(sink.calls() == before + 1);
  }

  {
    RecordingDisplay sink;
    PresentationDisplay display(sink);
    display.show(UiState::ready, "READY");
    display.set_context(10, 1);
    assert(display.show_attention(PresentationDomain::card, UiState::ready,
                                  "TURN CARD", PresentationScope::generation,
                                  10, 1));
    assert(display.model().domain == PresentationDomain::card);

    // Advancing generation invalidates both reducer state and adapter
    // bookkeeping, so the retired card cannot be cleared/resurrected later.
    display.set_context(10, 2);
    assert(display.model().surface == PresentationModel::Surface::base);
    assert(sink.text() == "READY");
    assert(!display.clear_attention(PresentationDomain::card));
  }

  {
    RecordingDisplay sink;
    PresentationDisplay display(sink);
    display.show(UiState::ready, "READY");
    display.set_context(20, 1);
    assert(display.show_attention(PresentationDomain::pairing, UiState::ready,
                                  "PAIR CURRENT"));
    assert(display.show_attention(PresentationDomain::confirmation, UiState::ready,
                                  "CONFIRM"));

    // A stale scoped update is rejected before it can mutate the adapter's
    // renderer-state cache. Revealing the existing pairing candidate therefore
    // keeps its original renderer state/text.
    assert(!display.show_attention(PresentationDomain::pairing, UiState::error,
                                   "PAIR STALE", PresentationScope::session,
                                   19, 0));
    assert(display.clear_attention(PresentationDomain::confirmation));
    assert(sink.state() == UiState::ready);
    assert(sink.text() == "PAIR CURRENT");
  }

  {
    RecordingDisplay sink;
    PresentationDisplay display(sink);
    display.show(UiState::idle, "IDLE");
    PresentationCardV1 card{};
    assert(card.assign(1, "expense_summary", "THIS WEEK", "1,250,000 VND",
                       "UNDER BUDGET", 42));
    assert(display.show_card(UiState::idle, card));
    assert(display.model().domain == PresentationDomain::card);
    assert(sink.state() == UiState::idle);
    assert(sink.text() == "1,250,000 VND");

    // The local runtime remains authoritative; its next base render retires the
    // informational card rather than creating a permanent second UI path.
    display.show(UiState::ready, "PRESS TO TALK");
    assert(display.model().surface == PresentationModel::Surface::base);
    assert(sink.state() == UiState::ready);
    assert(sink.text() == "PRESS TO TALK");
  }

  {
    RecordingDisplay sink;
    PresentationDisplay display(sink);
    display.show(UiState::processing, "PROCESSING");

    PresentationHint hint{};
    assert(hint.assign("thinking", {}));
    assert(display.show_hint(UiState::processing, hint));
    assert(sink.state() == UiState::processing);
    assert(sink.text() == "THINKING");
    assert(display.model().activity == PresentationActivity::thinking);

    assert(hint.assign("speaking", {}));
    assert(!display.show_hint(UiState::processing, hint));
    assert(sink.state() == UiState::processing);
    assert(sink.text() == "THINKING");

    assert(hint.assign("tool_executing", "query_expenses"));
    assert(display.show_hint(UiState::processing, hint));
    assert(sink.state() == UiState::processing);
    assert(sink.text() == "query_expenses");

    // Backend error/interrupted hints never replace local failure/lifecycle truth.
    assert(hint.assign("error", {}));
    assert(!display.show_hint(UiState::processing, hint));
    assert(sink.state() == UiState::processing);
  }

  {
    RecordingDisplay sink;
    PresentationDisplay display(sink);
    display.show(UiState::processing, "PROCESSING");
    AgentPresentationStatus status{};
    assert(status.assign("retrieving memory"));
    assert(display.show_agent_status(UiState::processing, status));
    assert(sink.state() == UiState::processing);
    assert(sink.text() == "retrieving memory");

    display.show(UiState::ready, "READY");
    assert(!display.show_agent_status(UiState::ready, status));
    assert(sink.state() == UiState::ready);
    assert(sink.text() == "READY");
  }

  return 0;
}
