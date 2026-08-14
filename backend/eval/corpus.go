package eval

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// LoadCorpus reads JSONL without depending on file-system state. IDs omitted by
// the legacy corpus are assigned from their stable line number.
func LoadCorpus(r io.Reader) ([]Scenario, string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, "", fmt.Errorf("read corpus: %w", err)
	}
	sum := sha256.Sum256(data)
	scenarios := make([]Scenario, 0)
	seen := make(map[string]struct{})
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for line := 1; scanner.Scan(); line++ {
		raw := bytes.TrimSpace(scanner.Bytes())
		if len(raw) == 0 {
			continue
		}
		var scenario Scenario
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&scenario); err != nil {
			return nil, "", fmt.Errorf("decode corpus line %d: %w", line, err)
		}
		if err := normalizeScenario(&scenario, line); err != nil {
			return nil, "", err
		}
		if _, ok := seen[scenario.ID]; ok {
			return nil, "", fmt.Errorf("corpus line %d: duplicate scenario id %q", line, scenario.ID)
		}
		seen[scenario.ID] = struct{}{}
		scenarios = append(scenarios, scenario)
	}
	if err := scanner.Err(); err != nil {
		return nil, "", fmt.Errorf("scan corpus: %w", err)
	}
	if len(scenarios) == 0 {
		return nil, "", fmt.Errorf("corpus is empty")
	}
	return scenarios, hex.EncodeToString(sum[:]), nil
}

func normalizeScenario(s *Scenario, line int) error {
	s.ID = strings.TrimSpace(s.ID)
	if s.ID == "" {
		s.ID = fmt.Sprintf("scenario-%04d", line)
	}
	s.Input = strings.TrimSpace(s.Input)
	if s.Input == "" {
		return fmt.Errorf("corpus line %d (%s): input is required", line, s.ID)
	}
	if s.Kind == "" {
		s.Kind = "routing"
	}
	if s.Fallback && (len(s.MustPack) > 0 || len(s.Expect.Packs) > 0) {
		return fmt.Errorf("corpus line %d (%s): fallback cannot require packs", line, s.ID)
	}
	if s.Expect.RetrievalK < 0 {
		return fmt.Errorf("corpus line %d (%s): retrieval_k must be non-negative", line, s.ID)
	}
	for i := range s.Tools {
		if s.Tools[i].Type == "" {
			s.Tools[i].Type = "function"
		}
		if s.Tools[i].Type != "function" {
			return fmt.Errorf("corpus line %d (%s): tool %d has unsupported type %q", line, s.ID, i, s.Tools[i].Type)
		}
		if strings.TrimSpace(s.Tools[i].Function.Name) == "" {
			return fmt.Errorf("corpus line %d (%s): tool %d name is required", line, s.ID, i)
		}
	}
	return nil
}

func expectedPacks(s Scenario) ([]string, bool, bool) {
	if s.Fallback {
		return []string{}, true, true
	}
	if s.Expect.Packs != nil {
		return s.Expect.Packs, true, s.Expect.ExactPacks
	}
	if s.MustPack != nil {
		return s.MustPack, true, s.Exact
	}
	return nil, false, false
}
