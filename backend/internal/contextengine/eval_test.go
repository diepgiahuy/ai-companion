package contextengine

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"testing"
)

type evalScenario struct {
	Input    string   `json:"input"`
	MustPack []string `json:"must_pack"`
	Exact    bool     `json:"exact"`
	Fallback bool     `json:"fallback"`
}

func TestRoutingEvaluationCorpus(t *testing.T) {
	f, err := os.Open("../../eval/scenarios.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r := New(nil)
	scan := bufio.NewScanner(f)
	line := 0
	for scan.Scan() {
		line++
		var s evalScenario
		if err := json.Unmarshal(scan.Bytes(), &s); err != nil {
			t.Fatalf("line %d: %v", line, err)
		}
		got := r.Plan(context.Background(), s.Input).Packs
		have := map[string]bool{}
		for _, p := range got {
			have[p] = true
		}
		for _, p := range s.MustPack {
			if !have[p] {
				t.Errorf("line %d %q: packs=%v missing=%s", line, s.Input, got, p)
			}
		}
		if s.Exact && len(got) != len(s.MustPack) {
			t.Errorf("line %d %q: expected exact %v got %v", line, s.Input, s.MustPack, got)
		}
		if s.Fallback && len(got) != 7 {
			t.Errorf("line %d %q: fallback packs=%v", line, s.Input, got)
		}
	}
	if err := scan.Err(); err != nil {
		t.Fatal(err)
	}
	if line < 40 {
		t.Fatalf("evaluation corpus too small: %d", line)
	}
}
