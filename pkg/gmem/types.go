package gmem

import "encoding/json"

// mapToJSON serializes a map as JSON string (FalkorDB properties don't support nested maps)
func mapToJSON(m map[string]any) (string, error) {
	if m == nil {
		return "{}", nil
	}
	b, err := json.Marshal(m)
	return string(b), err
}

// jsonToMap deserializes a JSON string into a map; empty string returns empty map
func jsonToMap(s string) map[string]any {
	m := map[string]any{}
	if s != "" {
		_ = json.Unmarshal([]byte(s), &m)
	}
	return m
}

// strSlice converts []any from FalkorDB to []string
func strSlice(v any) []string {
	out := []string{}
	if arr, ok := v.([]any); ok {
		for _, x := range arr {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// contains reports whether ss contains s
func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
