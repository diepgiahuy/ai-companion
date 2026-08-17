package presentation

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCardV1CanonicalShapeAndBounds(t *testing.T) {
	card, err := NewCardV1("expense_summary", "This week", "1,250,000 VND", "under budget", 42)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"version":1,"kind":"expense_summary","title":"This week","primary":"1,250,000 VND","secondary":"under budget","progress":42}`
	if string(raw) != want {
		t.Fatalf("card wire = %s, want %s", raw, want)
	}
}

func TestCardV1RejectsUnboundedOrInvalidData(t *testing.T) {
	tests := []struct {
		name string
		card CardV1
	}{
		{name: "wrong version", card: CardV1{Version: 2, Kind: "text"}},
		{name: "blank kind", card: CardV1{Version: 1, Kind: "   "}},
		{name: "kind punctuation", card: CardV1{Version: 1, Kind: "text/card"}},
		{name: "kind too long", card: CardV1{Version: 1, Kind: strings.Repeat("k", 33)}},
		{name: "title too long", card: CardV1{Version: 1, Kind: "text", Title: strings.Repeat("t", 97)}},
		{name: "primary too long", card: CardV1{Version: 1, Kind: "text", Primary: strings.Repeat("p", 193)}},
		{name: "secondary too long", card: CardV1{Version: 1, Kind: "text", Secondary: strings.Repeat("s", 193)}},
		{name: "invalid utf8", card: CardV1{Version: 1, Kind: "text", Primary: string([]byte{0xff})}},
		{name: "control character", card: CardV1{Version: 1, Kind: "text", Primary: "line one\nline two"}},
		{name: "nul character", card: CardV1{Version: 1, Kind: "text", Primary: "safe\x00truncated"}},
		{name: "progress negative", card: CardV1{Version: 1, Kind: "progress", Progress: -1}},
		{name: "progress over 100", card: CardV1{Version: 1, Kind: "progress", Progress: 101}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.card.Validate(); err == nil {
				t.Fatal("invalid card was accepted")
			}
		})
	}
}

func TestCardV1AllowsUnknownKindOnlyAsBoundedData(t *testing.T) {
	card, err := NewCardV1("future_card", "", "safe fallback", "", 0)
	if err != nil {
		t.Fatalf("bounded tagged card rejected: %v", err)
	}
	if card.Kind != "future_card" || card.Primary != "safe fallback" {
		t.Fatalf("unexpected card: %+v", card)
	}
}
