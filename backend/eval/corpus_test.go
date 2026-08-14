package eval

import (
	"strings"
	"testing"
)

func TestLoadCorpusPreservesLegacyRoutingFields(t *testing.T) {
	corpus := "{\"input\":\"Hẹn giờ\",\"must_pack\":[\"schedule\"]}\n" +
		"{\"id\":\"fallback\",\"input\":\"Xin chào\",\"fallback\":true}\n"
	scenarios, digest, err := LoadCorpus(strings.NewReader(corpus))
	if err != nil {
		t.Fatal(err)
	}
	if len(scenarios) != 2 || scenarios[0].ID != "scenario-0001" || scenarios[0].Kind != "routing" {
		t.Fatalf("scenarios=%+v", scenarios)
	}
	if len(digest) != 64 {
		t.Fatalf("digest=%q", digest)
	}
	metrics := Score(scenarios[0], Observation{Packs: []string{"schedule", "budget"}})
	if metrics.PackSelection == nil || metrics.PackSelection.FalseNegative != 0 {
		t.Fatalf("pack metrics=%+v", metrics.PackSelection)
	}
	if metrics.TaskSuccess == nil || !*metrics.TaskSuccess {
		t.Fatalf("must_pack should allow additional packs: %+v", metrics.TaskSuccess)
	}
}

func TestLoadCorpusRejectsDuplicateIDs(t *testing.T) {
	_, _, err := LoadCorpus(strings.NewReader("{\"id\":\"same\",\"input\":\"one\"}\n{\"id\":\"same\",\"input\":\"two\"}\n"))
	if err == nil || !strings.Contains(err.Error(), "duplicate scenario id") {
		t.Fatalf("error=%v", err)
	}
}
