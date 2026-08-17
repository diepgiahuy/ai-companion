package eval

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"sort"
	"strings"
)

func Score(s Scenario, observation Observation) QualityMetrics {
	metrics := QualityMetrics{}
	var scores []float64
	var checks []bool

	if packs, ok, exact := expectedPacks(s); ok {
		set := scoreSet(packs, observation.Packs, true)
		metrics.PackSelection = &set
		scores = append(scores, set.F1)
		if exact {
			checks = append(checks, set.Exact)
		} else {
			checks = append(checks, set.FalseNegative == 0)
		}
	}

	expectedToolNames := make([]string, 0, len(s.Expect.ToolCalls))
	for _, call := range s.Expect.ToolCalls {
		expectedToolNames = append(expectedToolNames, call.Name)
	}
	observedToolNames := make([]string, 0, len(observation.ToolCalls))
	for _, call := range observation.ToolCalls {
		observedToolNames = append(observedToolNames, call.Name)
	}
	if s.Expect.ToolCalls != nil || s.Expect.NoToolCall {
		set := scoreSet(expectedToolNames, observedToolNames, false)
		metrics.ToolSelection = &set
		scores = append(scores, set.F1)
		checks = append(checks, set.Exact)
	}
	if len(s.Expect.ToolCalls) > 0 {
		argumentMatch := scoreArguments(s.Expect.ToolCalls, observation.ToolCalls)
		metrics.ArgumentMatch = &argumentMatch
		scores = append(scores, argumentMatch.Rate)
		checks = append(checks, argumentMatch.Correct == argumentMatch.Evaluated)
	}
	if len(observation.ToolCalls) > 0 {
		schemaValidity := scoreSchemas(s.Tools, observation.ToolCalls)
		metrics.SchemaValidity = &schemaValidity
		scores = append(scores, schemaValidity.Rate)
		checks = append(checks, schemaValidity.Correct == schemaValidity.Evaluated)
	}

	forbidden := make(map[string]struct{}, len(s.Expect.ForbiddenTools))
	for _, name := range s.Expect.ForbiddenTools {
		forbidden[strings.TrimSpace(name)] = struct{}{}
	}
	for _, call := range observation.ToolCalls {
		if _, found := forbidden[strings.TrimSpace(call.Name)]; found {
			metrics.ForbiddenToolCalls++
		}
	}
	if len(s.Expect.ForbiddenTools) > 0 {
		checks = append(checks, metrics.ForbiddenToolCalls == 0)
	}

	if content := scoreContent(s.Expect, observation.Text); content != nil {
		metrics.ContentChecks = content
		scores = append(scores, content.Rate)
		checks = append(checks, content.Correct == content.Evaluated)
	}
	if len(s.Expect.RetrievalIDs) > 0 {
		retrieval := scoreRetrieval(s.Expect.RetrievalIDs, observation.RetrievedIDs, s.Expect.RetrievalK)
		metrics.Retrieval = &retrieval
		scores = append(scores, (retrieval.RecallAtK+retrieval.NDCG)/2)
		checks = append(checks, retrieval.RecallAtK == 1)
	}
	if s.Expect.Escalate != nil {
		correct := observation.Escalate != nil && *observation.Escalate == *s.Expect.Escalate
		metrics.EscalationCorrect = &correct
		if correct {
			scores = append(scores, 1)
		} else {
			scores = append(scores, 0)
		}
		checks = append(checks, correct)
	}
	if len(scores) > 0 {
		score := mean(scores)
		metrics.DeterministicScore = &score
	}
	if len(checks) > 0 {
		success := true
		for _, check := range checks {
			success = success && check
		}
		metrics.TaskSuccess = &success
	}
	return metrics
}

func scoreSet(expected, observed []string, foldCase bool) SetMetrics {
	normalize := func(value string) string {
		value = strings.TrimSpace(value)
		if foldCase {
			return strings.ToLower(value)
		}
		return value
	}
	expectedCount := make(map[string]int)
	observedCount := make(map[string]int)
	for _, value := range expected {
		expectedCount[normalize(value)]++
	}
	for _, value := range observed {
		observedCount[normalize(value)]++
	}
	result := SetMetrics{
		Expected: append([]string(nil), expected...),
		Observed: append([]string(nil), observed...),
	}
	sort.Strings(result.Expected)
	sort.Strings(result.Observed)
	for value, count := range expectedCount {
		matched := min(count, observedCount[value])
		result.TruePositive += matched
		result.FalseNegative += count - matched
	}
	for value, count := range observedCount {
		result.FalsePositive += count - min(count, expectedCount[value])
	}
	result.Exact = result.FalsePositive == 0 && result.FalseNegative == 0
	if len(observed) == 0 {
		if len(expected) == 0 {
			result.Precision = 1
		}
	} else {
		result.Precision = float64(result.TruePositive) / float64(len(observed))
	}
	if len(expected) == 0 {
		result.Recall = 1
	} else {
		result.Recall = float64(result.TruePositive) / float64(len(expected))
	}
	if result.Precision+result.Recall > 0 {
		result.F1 = 2 * result.Precision * result.Recall / (result.Precision + result.Recall)
	}
	if len(expected) == 0 && len(observed) == 0 {
		result.F1 = 1
	}
	return result
}

func scoreArguments(expected []ExpectedToolCall, observed []ToolCall) RateMetric {
	result := RateMetric{Evaluated: len(expected)}
	used := make([]bool, len(observed))
	for _, want := range expected {
		for i, got := range observed {
			if used[i] || strings.TrimSpace(got.Name) != strings.TrimSpace(want.Name) {
				continue
			}
			var arguments map[string]any
			decoder := json.NewDecoder(bytes.NewReader(got.Arguments))
			decoder.UseNumber()
			if err := decoder.Decode(&arguments); err != nil {
				continue
			}
			if want.Arguments != nil && !isSubset(want.Arguments, arguments) {
				continue
			}
			used[i] = true
			result.Correct++
			break
		}
	}
	if result.Evaluated > 0 {
		result.Rate = float64(result.Correct) / float64(result.Evaluated)
	}
	return result
}

func scoreSchemas(definitions []ToolDefinition, calls []ToolCall) RateMetric {
	result := RateMetric{Evaluated: len(calls)}
	byName := make(map[string]map[string]any, len(definitions))
	for _, definition := range definitions {
		byName[definition.Function.Name] = definition.Function.Parameters
	}
	for _, call := range calls {
		schema, found := byName[call.Name]
		if !found {
			continue
		}
		var value any
		decoder := json.NewDecoder(bytes.NewReader(call.Arguments))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			continue
		}
		if validateSchema(schema, value) {
			result.Correct++
		}
	}
	if result.Evaluated > 0 {
		result.Rate = float64(result.Correct) / float64(result.Evaluated)
	}
	return result
}

// validateSchema intentionally supports only the deterministic subset used by
// function tools: type, required, properties, additionalProperties, enum,
// numeric bounds, and array item/count constraints. Unsupported keywords do not
// become fabricated validation claims.
func validateSchema(schema map[string]any, value any) bool {
	if len(schema) == 0 {
		return true
	}
	switch enum := schema["enum"].(type) {
	case []any:
		matched := false
		for _, candidate := range enum {
			if valuesEqual(candidate, value) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	case []string:
		matched := false
		for _, candidate := range enum {
			if valuesEqual(candidate, value) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	typeName, _ := schema["type"].(string)
	switch typeName {
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return false
		}
		properties, _ := schema["properties"].(map[string]any)
		switch required := schema["required"].(type) {
		case []any:
			for _, raw := range required {
				name, ok := raw.(string)
				if !ok {
					return false
				}
				if _, found := object[name]; !found {
					return false
				}
			}
		case []string:
			for _, name := range required {
				if _, found := object[name]; !found {
					return false
				}
			}
		}
		for name, child := range object {
			rawChildSchema, found := properties[name]
			if !found {
				if allowed, ok := schema["additionalProperties"].(bool); ok && !allowed {
					return false
				}
				if childSchema, ok := schema["additionalProperties"].(map[string]any); ok {
					if !validateSchema(childSchema, child) {
						return false
					}
				}
				continue
			}
			childSchema, ok := rawChildSchema.(map[string]any)
			if !ok || !validateSchema(childSchema, child) {
				return false
			}
		}
	case "array":
		items, ok := value.([]any)
		if !ok {
			return false
		}
		if minimum, ok := number(schema["minItems"]); ok && float64(len(items)) < minimum {
			return false
		}
		if maximum, ok := number(schema["maxItems"]); ok && float64(len(items)) > maximum {
			return false
		}
		if childSchema, ok := schema["items"].(map[string]any); ok {
			for _, item := range items {
				if !validateSchema(childSchema, item) {
					return false
				}
			}
		}
	case "string":
		if _, ok := value.(string); !ok {
			return false
		}
	case "number":
		if _, ok := number(value); !ok {
			return false
		}
	case "integer":
		value, ok := number(value)
		if !ok || math.Trunc(value) != value {
			return false
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return false
		}
	case "null":
		if value != nil {
			return false
		}
	case "":
	default:
		return false
	}
	if numeric, ok := number(value); ok {
		if minimum, ok := number(schema["minimum"]); ok && numeric < minimum {
			return false
		}
		if maximum, ok := number(schema["maximum"]); ok && numeric > maximum {
			return false
		}
	}
	return true
}

func scoreContent(expect Expectations, text string) *RateMetric {
	evaluated := len(expect.MustContain) + len(expect.MustNotContain)
	if expect.OutputExact != "" {
		evaluated++
	}
	if evaluated == 0 {
		return nil
	}
	metric := &RateMetric{Evaluated: evaluated}
	if expect.OutputExact != "" && text == expect.OutputExact {
		metric.Correct++
	}
	lower := strings.ToLower(text)
	for _, required := range expect.MustContain {
		if strings.Contains(lower, strings.ToLower(required)) {
			metric.Correct++
		}
	}
	for _, forbidden := range expect.MustNotContain {
		if !strings.Contains(lower, strings.ToLower(forbidden)) {
			metric.Correct++
		}
	}
	metric.Rate = float64(metric.Correct) / float64(metric.Evaluated)
	return metric
}

func scoreRetrieval(expected, observed []string, k int) RetrievalMetrics {
	if k <= 0 {
		k = 5
	}
	limit := min(k, len(observed))
	top := append([]string(nil), observed[:limit]...)
	relevant := make(map[string]struct{}, len(expected))
	for _, id := range expected {
		relevant[id] = struct{}{}
	}
	seenRelevant := make(map[string]struct{}, len(relevant))
	hits := 0
	dcg := 0.0
	for i, id := range top {
		_, isRelevant := relevant[id]
		_, alreadySeen := seenRelevant[id]
		if isRelevant && !alreadySeen {
			seenRelevant[id] = struct{}{}
			hits++
			dcg += 1 / math.Log2(float64(i+2))
		}
	}
	idcg := 0.0
	for i := 0; i < min(len(relevant), k); i++ {
		idcg += 1 / math.Log2(float64(i+2))
	}
	ndcg := 0.0
	if idcg > 0 {
		ndcg = dcg / idcg
	}
	return RetrievalMetrics{
		Expected:  append([]string(nil), expected...),
		Observed:  top,
		K:         k,
		RecallAtK: float64(hits) / float64(len(relevant)),
		NDCG:      ndcg,
	}
}

func isSubset(expected, observed any) bool {
	expectedMap, isMap := expected.(map[string]any)
	if isMap {
		observedMap, ok := observed.(map[string]any)
		if !ok {
			return false
		}
		for key, value := range expectedMap {
			got, found := observedMap[key]
			if !found || !isSubset(value, got) {
				return false
			}
		}
		return true
	}
	expectedSlice, isSlice := expected.([]any)
	if isSlice {
		observedSlice, ok := observed.([]any)
		if !ok || len(expectedSlice) != len(observedSlice) {
			return false
		}
		for i := range expectedSlice {
			if !isSubset(expectedSlice[i], observedSlice[i]) {
				return false
			}
		}
		return true
	}
	return valuesEqual(expected, observed)
}

func valuesEqual(left, right any) bool {
	leftNumber, leftOK := number(left)
	rightNumber, rightOK := number(right)
	if leftOK && rightOK {
		return leftNumber == rightNumber
	}
	return reflect.DeepEqual(left, right)
}

func number(value any) (float64, bool) {
	switch value := value.(type) {
	case json.Number:
		number, err := value.Float64()
		return number, err == nil
	case float64:
		return value, true
	case float32:
		return float64(value), true
	case int:
		return float64(value), true
	case int64:
		return float64(value), true
	default:
		return 0, false
	}
}

func mean(values []float64) float64 {
	total := 0.0
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}
