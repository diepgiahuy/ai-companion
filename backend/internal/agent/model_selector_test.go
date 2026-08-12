package agent

import "testing"

func TestKeywordModelSelector(t *testing.T) {
	s := KeywordModelSelector{Fast: "fast", Reasoning: "reason"}
	if s.Select("lưu 50k tiền ăn") != "fast" {
		t.Fatal("CRUD should be fast")
	}
	if s.Select("phân tích chi tiêu tháng này") != "reason" {
		t.Fatal("analysis should route reasoning")
	}
}
