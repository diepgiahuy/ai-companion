package idempotency

import (
	"errors"
	"testing"
)

func TestHashJSONCanonicalizesKeyOrderAndWhitespace(t *testing.T) {
	left, err := HashJSON(`{"amount_vnd":50000,"meta":{"b":2,"a":1}}`)
	if err != nil {
		t.Fatal(err)
	}
	right, err := HashJSON(` { "meta" : { "a" : 1, "b" : 2 }, "amount_vnd" : 50000 } `)
	if err != nil {
		t.Fatal(err)
	}
	if !EqualHash(left, right) {
		t.Fatalf("semantic JSON should hash equally: %s != %s", left, right)
	}
}

func TestHashJSONDistinguishesNumericSemantics(t *testing.T) {
	one, err := HashJSON(`{"delay_seconds":1}`)
	if err != nil {
		t.Fatal(err)
	}
	two, err := HashJSON(`{"delay_seconds":2}`)
	if err != nil {
		t.Fatal(err)
	}
	if EqualHash(one, two) {
		t.Fatal("different semantic requests must not share a hash")
	}
}

func TestHashJSONRejectsTrailingValues(t *testing.T) {
	if _, err := HashJSON(`{"a":1} {"b":2}`); err == nil {
		t.Fatal("expected trailing JSON to be rejected")
	}
}

func TestRequestValidate(t *testing.T) {
	hash, err := HashJSON(`{"content":"note"}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := (Request{Actor: "tenant:user", Operation: "note.create", Key: "request-1", RequestHash: hash}).Validate(); err != nil {
		t.Fatal(err)
	}
	for _, request := range []Request{
		{Operation: "note.create", Key: "request-1", RequestHash: hash},
		{Actor: "user", Key: "request-1", RequestHash: hash},
		{Actor: "user", Operation: "note.create", RequestHash: hash},
		{Actor: "user", Operation: "note.create", Key: "request-1", RequestHash: "not-a-hash"},
	} {
		if err := request.Validate(); err == nil {
			t.Fatalf("invalid request unexpectedly passed: %+v", request)
		}
	}
}

func TestConflictHasStableCodeWithoutPayload(t *testing.T) {
	err := Conflict{Operation: "note.create", Key: "request-1"}
	if !IsConflict(err) {
		t.Fatal("conflict classification failed")
	}
	if !errors.Is(err, err) {
		t.Fatal("conflict should remain a normal Go error")
	}
	if got := err.Error(); got != "IDEMPOTENCY_CONFLICT: idempotency key was already committed with a different request" {
		t.Fatalf("unexpected conflict text %q", got)
	}
}
