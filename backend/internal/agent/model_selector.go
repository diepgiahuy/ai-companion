package agent

import "strings"

type ModelSelector interface {
	Select(transcript string) string
}
type KeywordModelSelector struct{ Fast, Reasoning string }

func (s KeywordModelSelector) Select(q string) string {
	q = strings.ToLower(q)
	for _, w := range []string{"phân tích", "so sánh", "tại sao", "kế hoạch", "tư vấn", "đánh giá", "review", "analyze", "compare", "reason", "optimize", "tối ưu"} {
		if strings.Contains(q, w) && strings.TrimSpace(s.Reasoning) != "" {
			return s.Reasoning
		}
	}
	if strings.TrimSpace(s.Fast) != "" {
		return s.Fast
	}
	return s.Reasoning
}
