#include "companion/presentation.hpp"

#include <cassert>
#include <string>

using namespace companion;

namespace {
PresentationEvent base(PresentationActivity activity, uint64_t revision,
                       std::string_view text,
                       PresentationScope scope = PresentationScope::global,
                       uint64_t session = 0, uint64_t generation = 0) {
  return PresentationEvent{
      .kind = PresentationEvent::Kind::base,
      .activity = activity,
      .scope = scope,
      .session_epoch = session,
      .generation = generation,
      .revision = revision,
      .text = text,
  };
}

PresentationEvent attention(PresentationDomain domain, uint64_t revision,
                            std::string_view text,
                            PresentationScope scope = PresentationScope::global,
                            uint64_t session = 0, uint64_t generation = 0) {
  return PresentationEvent{
      .kind = PresentationEvent::Kind::attention,
      .domain = domain,
      .scope = scope,
      .session_epoch = session,
      .generation = generation,
      .revision = revision,
      .text = text,
  };
}

PresentationEvent clear(PresentationDomain domain, uint64_t revision,
                        PresentationScope scope = PresentationScope::global,
                        uint64_t session = 0, uint64_t generation = 0) {
  return PresentationEvent{
      .kind = PresentationEvent::Kind::clear_attention,
      .domain = domain,
      .scope = scope,
      .session_epoch = session,
      .generation = generation,
      .revision = revision,
  };
}
} // namespace

int main() {
  {
    // Revision 0 is a valid first domain revision. The reducer's synthetic
    // boot snapshot must not reserve it as an implicit sentinel.
    PresentationReducer reducer;
    assert(reducer.apply(base(PresentationActivity::ready, 0, "READY")));
    assert(reducer.model().text_view() == "READY");
    assert(reducer.apply(attention(PresentationDomain::card, 0, "CARD-0")));
    assert(reducer.model().domain == PresentationDomain::card);
  }

  {
    PresentationReducer reducer;
    assert(reducer.apply(base(PresentationActivity::speaking, 1, "SPEAKING")));
    assert(reducer.apply(attention(PresentationDomain::card, 1, "CARD")));
    assert(reducer.model().surface == PresentationModel::Surface::base);
    assert(reducer.model().activity == PresentationActivity::speaking);

    assert(reducer.apply(attention(PresentationDomain::alarm, 1, "ALARM")));
    assert(reducer.model().surface == PresentationModel::Surface::attention);
    assert(reducer.model().domain == PresentationDomain::alarm);

    assert(reducer.apply(attention(PresentationDomain::pairing, 1, "PAIR")));
    assert(reducer.model().domain == PresentationDomain::pairing);

    assert(reducer.apply(attention(PresentationDomain::confirmation, 1, "CONFIRM")));
    assert(reducer.model().domain == PresentationDomain::confirmation);

    // Overlay ownership does not freeze the runtime activity underneath it.
    assert(reducer.apply(base(PresentationActivity::error, 2, "ERROR")));
    assert(reducer.model().domain == PresentationDomain::confirmation);
    assert(reducer.model().base_activity == PresentationActivity::error);
    assert(reducer.model().activity == PresentationActivity::error);

    assert(reducer.apply(clear(PresentationDomain::confirmation, 1)));
    assert(reducer.model().domain == PresentationDomain::pairing);
    assert(reducer.apply(clear(PresentationDomain::pairing, 1)));
    assert(reducer.model().domain == PresentationDomain::alarm);
    assert(reducer.apply(clear(PresentationDomain::alarm, 1)));
    assert(reducer.model().surface == PresentationModel::Surface::base);
    assert(reducer.model().activity == PresentationActivity::error);
  }

  {
    // Revisions are monotonic only inside one owning domain. Cross-domain ties
    // use the reducer's fixed authority order, never incomparable revision IDs.
    PresentationReducer reducer;
    assert(reducer.apply(attention(PresentationDomain::pairing, 999, "PAIR")));
    assert(reducer.apply(attention(PresentationDomain::ota, 1, "OTA")));
    assert(reducer.model().domain == PresentationDomain::ota);
    assert(reducer.apply(attention(PresentationDomain::confirmation, 500, "CONFIRM")));
    assert(reducer.apply(attention(PresentationDomain::privacy, 0, "PRIVACY")));
    assert(reducer.model().domain == PresentationDomain::privacy);
  }

  {
    PresentationReducer reducer;
    reducer.set_context(11, 4);
    assert(reducer.apply(base(PresentationActivity::speaking, 1, "GEN4",
                              PresentationScope::generation, 11, 4)));
    assert(!reducer.apply(base(PresentationActivity::thinking, 2, "STALE",
                               PresentationScope::generation, 11, 3)));
    assert(reducer.counters().stale_drops == 1);
    assert(reducer.model().text_view() == "GEN4");

    reducer.set_context(11, 5);
    assert(reducer.model().base_activity == PresentationActivity::booting);
    assert(!reducer.apply(attention(PresentationDomain::card, 1, "OLD CARD",
                                    PresentationScope::generation, 11, 4)));
    assert(reducer.apply(base(PresentationActivity::listening, 1, "GEN5",
                              PresentationScope::generation, 11, 5)));
    assert(reducer.model().text_view() == "GEN5");
  }

  {
    PresentationReducer reducer;
    reducer.set_context(20, 7);
    assert(reducer.apply(attention(PresentationDomain::alarm, 9, "ALARM",
                                   PresentationScope::global)));
    assert(reducer.apply(attention(PresentationDomain::pairing, 3, "PAIRING",
                                   PresentationScope::session, 20, 0)));
    assert(reducer.apply(attention(PresentationDomain::card, 4, "TURN CARD",
                                   PresentationScope::generation, 20, 7)));

    reducer.set_context(21, 1);
    assert(reducer.model().domain == PresentationDomain::alarm);
    assert(!reducer.apply(clear(PresentationDomain::pairing, 4,
                                PresentationScope::session, 20, 0)));
    assert(reducer.counters().stale_drops == 1);
  }

  {
    PresentationReducer reducer;
    assert(reducer.apply(attention(PresentationDomain::voice_mail, 10, "MAIL-10")));
    assert(!reducer.apply(attention(PresentationDomain::voice_mail, 9, "MAIL-9")));
    assert(!reducer.apply(attention(PresentationDomain::voice_mail, 10, "MAIL-10")));
    assert(!reducer.apply(attention(PresentationDomain::voice_mail, 10, "CONFLICT")));
    assert(reducer.apply(attention(PresentationDomain::voice_mail, 11, "MAIL-11")));
    assert(reducer.counters().stale_drops == 1);
    assert(reducer.counters().duplicate_drops == 1);
    assert(reducer.counters().revision_conflicts == 1);
    assert(reducer.counters().coalesced_updates == 1);
    assert(reducer.model().text_view() == "MAIL-11");
  }

  {
    PresentationReducer reducer;
    std::string oversized(140, 'x');
    assert(reducer.apply(attention(PresentationDomain::card, 1, oversized)));
    assert(reducer.model().text_view().size() == 96);
    assert(reducer.counters().truncated_text == 1);
    assert(!reducer.apply(attention(PresentationDomain::card, 1, oversized)));
    assert(reducer.counters().duplicate_drops == 1);
    assert(reducer.counters().revision_conflicts == 0);
    assert(reducer.counters().truncated_text == 1);
  }

  {
    // The bound is in bytes, but valid Vietnamese/UTF-8 content must never be
    // cut in the middle of a multi-byte code point.
    PresentationReducer reducer;
    std::string utf8(95, 'x');
    utf8 += "\xC3\xA9"; // U+00E9; total 97 bytes, proposed byte 96 is continuation.
    assert(reducer.apply(attention(PresentationDomain::card, 1, utf8)));
    assert(reducer.model().text_view().size() == 95);
    assert(reducer.model().text_view() == std::string_view(utf8.data(), 95));
    assert(reducer.counters().truncated_text == 1);
  }

  return 0;
}
