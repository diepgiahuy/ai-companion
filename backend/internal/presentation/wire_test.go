package presentation_test

import (
	"encoding/json"
	"testing"

	"companion-server/internal/presentation"
	"companion-server/internal/protocol"
)

func TestCardV1EncodesAsBoundedUICardPayload(t *testing.T) {
	card, err := presentation.NewCardV1("expense_summary", "This week", "1,250,000 VND", "under budget", 42)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := protocol.Encode(protocol.UICardType, protocol.Metadata{
		MessageID: "card-1",
		SessionID: "session-1",
		TurnID:    "turn-1",
	}, protocol.UICardPayload{UI: card})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := protocol.Decode(wire)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		UI presentation.CardV1 `json:"ui"`
	}
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.UI != card {
		t.Fatalf("decoded card = %+v, want %+v", payload.UI, card)
	}
}
