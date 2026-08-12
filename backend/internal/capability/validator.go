package capability

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// ValidateArguments validates the JSON-schema subset deliberately used by
// companion tools. Model-side constrained decoding is only a convenience;
// host-side validation remains the security/correctness boundary.
func ValidateArguments(schema map[string]any, raw string) error {
	if schema == nil {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return fmt.Errorf("invalid JSON arguments: %w", err)
	}
	if dec.More() {
		return fmt.Errorf("multiple JSON values are not allowed")
	}
	return validateValue(schema, value, "$")
}

func validateValue(schema map[string]any, value any, path string) error {
	typ, _ := schema["type"].(string)
	switch typ {
	case "object":
		obj, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be object", path)
		}
		props, _ := schema["properties"].(map[string]any)
		req := map[string]bool{}
		if a, ok := schema["required"].([]string); ok {
			for _, x := range a {
				req[x] = true
			}
		}
		if a, ok := schema["required"].([]any); ok {
			for _, x := range a {
				if s, ok := x.(string); ok {
					req[s] = true
				}
			}
		}
		for k := range req {
			if _, ok := obj[k]; !ok {
				return fmt.Errorf("%s.%s is required", path, k)
			}
		}
		additional, hasAdditional := schema["additionalProperties"].(bool)
		for k, v := range obj {
			rawChild, exists := props[k]
			if !exists {
				if hasAdditional && !additional {
					return fmt.Errorf("%s.%s is not allowed", path, k)
				}
				continue
			}
			child, ok := rawChild.(map[string]any)
			if ok {
				if err := validateValue(child, v, path+"."+k); err != nil {
					return err
				}
			}
		}
	case "array":
		arr, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s must be array", path)
		}
		if n, ok := number(schema["minItems"]); ok && float64(len(arr)) < n {
			return fmt.Errorf("%s has too few items", path)
		}
		if n, ok := number(schema["maxItems"]); ok && float64(len(arr)) > n {
			return fmt.Errorf("%s has too many items", path)
		}
		if item, ok := schema["items"].(map[string]any); ok {
			for i, v := range arr {
				if err := validateValue(item, v, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	case "string":
		s, ok := value.(string)
		if !ok {
			return fmt.Errorf("%s must be string", path)
		}
		if n, ok := number(schema["minLength"]); ok && float64(len([]rune(s))) < n {
			return fmt.Errorf("%s is too short", path)
		}
		if n, ok := number(schema["maxLength"]); ok && float64(len([]rune(s))) > n {
			return fmt.Errorf("%s is too long", path)
		}
		if vals, ok := schema["enum"].([]string); ok && !contains(vals, s) {
			return fmt.Errorf("%s has invalid value", path)
		}
		if vals, ok := schema["enum"].([]any); ok {
			found := false
			for _, v := range vals {
				if v == s {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("%s has invalid value", path)
			}
		}
	case "integer":
		n, ok := number(value)
		if !ok || math.Trunc(n) != n {
			return fmt.Errorf("%s must be integer", path)
		}
		if min, ok := number(schema["minimum"]); ok && n < min {
			return fmt.Errorf("%s below minimum", path)
		}
		if max, ok := number(schema["maximum"]); ok && n > max {
			return fmt.Errorf("%s above maximum", path)
		}
	case "number":
		n, ok := number(value)
		if !ok {
			return fmt.Errorf("%s must be number", path)
		}
		if min, ok := number(schema["minimum"]); ok && n < min {
			return fmt.Errorf("%s below minimum", path)
		}
		if max, ok := number(schema["maximum"]); ok && n > max {
			return fmt.Errorf("%s above maximum", path)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s must be boolean", path)
		}
	}
	return nil
}
func number(v any) (float64, bool) {
	switch x := v.(type) {
	case json.Number:
		n, e := x.Float64()
		return n, e == nil
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case int32:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint64:
		return float64(x), true
	}
	return 0, false
}
func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
