#include "companion/presentation_ingress.hpp"

#include <cassert>
#include <string>
#include <string_view>

using namespace companion;

namespace {

void card_accepts_exact_contract_boundaries() {
  PresentationCardV1 card{};
  const std::string title(kPresentationCardTitleBytes, 't');
  const std::string text(kPresentationCardTextBytes, 'p');
  assert(card.assign(1, "expense_summary.v1", title, text, text, 0));
  assert(card.kind_view() == "expense_summary.v1");
  assert(card.title_view() == title);
  assert(card.primary_view() == text);
  assert(card.secondary_view() == text);
  assert(card.progress == 0);
  assert(card.assign(1, "saving-goal", "Tiết kiệm", "3.000.000 ₫", "đúng tiến độ", 100));
  assert(card.progress == 100);
}

void card_rejects_version_kind_bounds_and_progress() {
  PresentationCardV1 card{};
  assert(!card.assign(0, "expense", {}, {}, {}, 0));
  assert(!card.assign(2, "expense", {}, {}, {}, 0));
  assert(!card.assign(1, "", {}, {}, {}, 0));
  assert(!card.assign(1, "bad kind", {}, {}, {}, 0));
  assert(!card.assign(1, std::string(kPresentationCardKindBytes + 1, 'k'), {}, {}, {}, 0));
  assert(!card.assign(1, "expense", {}, {}, {}, -1));
  assert(!card.assign(1, "expense", {}, {}, {}, 101));
}

void card_rejects_overlong_invalid_utf8_and_control_text() {
  PresentationCardV1 card{};
  assert(!card.assign(1, "expense", std::string(kPresentationCardTitleBytes + 1, 't'), {}, {}, 0));
  assert(!card.assign(1, "expense", {}, std::string(kPresentationCardTextBytes + 1, 'p'), {}, 0));
  assert(!card.assign(1, "expense", {}, {}, std::string(kPresentationCardTextBytes + 1, 's'), 0));

  const std::string invalid_utf8{"\xC0\xAF", 2};
  assert(!card.assign(1, "expense", invalid_utf8, {}, {}, 0));
  assert(!card.assign(1, "expense", "line\nbreak", {}, {}, 0));
  const std::string unicode_control{"\xC2\x85", 2}; // U+0085 NEXT LINE, Unicode Cc.
  assert(!card.assign(1, "expense", unicode_control, {}, {}, 0));
}

void ui_hint_is_typed_and_bounded() {
  PresentationHint hint{};
  assert(hint.assign("idle", {}));
  assert(hint.emotion == PresentationHintEmotion::idle);
  assert(hint.assign("listening", {}));
  assert(hint.emotion == PresentationHintEmotion::listening);
  assert(hint.assign("thinking", {}));
  assert(hint.assign("speaking", {}));
  assert(hint.assign("interrupted", {}));
  assert(hint.assign("error", {}));
  assert(hint.assign("tool_executing", "query_expenses"));
  assert(hint.emotion == PresentationHintEmotion::tool_executing);
  assert(hint.tool_name_view() == "query_expenses");

  assert(!hint.assign("unknown", {}));
  assert(!hint.assign("tool_executing", {}));
  assert(!hint.assign("thinking", "unexpected_tool"));
  assert(!hint.assign("tool_executing", std::string(kPresentationHintTextBytes + 1, 'x')));
}

void agent_status_is_separate_bounded_semantic_data() {
  AgentPresentationStatus status{};
  assert(status.assign("thinking"));
  assert(status.state_view() == "thinking");
  assert(status.assign(std::string(kAgentStatusBytes, 's')));
  assert(!status.assign({}));
  assert(!status.assign(std::string(kAgentStatusBytes + 1, 's')));
  assert(!status.assign("bad\nstatus"));
}

} // namespace

int main() {
  card_accepts_exact_contract_boundaries();
  card_rejects_version_kind_bounds_and_progress();
  card_rejects_overlong_invalid_utf8_and_control_text();
  ui_hint_is_typed_and_bounded();
  agent_status_is_separate_bounded_semantic_data();
  return 0;
}
