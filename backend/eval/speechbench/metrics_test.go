package speechbench

import "testing"

func TestNormalizeTextPreservesVietnameseLetters(t *testing.T) {
	got := NormalizeText("  Tôi vừa chi 50.000₫, ăn trưa!  ")
	want := "tôi vừa chi 50 000 ăn trưa"
	if got != want { t.Fatalf("NormalizeText()=%q want=%q",got,want) }
}

func TestWERAndCER(t *testing.T) {
	if got:=WER("hello world","hello world"); got!=0 { t.Fatalf("WER exact=%v",got) }
	if got:=WER("hello world","hello"); got!=0.5 { t.Fatalf("WER deletion=%v",got) }
	if got:=CER("xin chào","xin chao"); got<=0 { t.Fatalf("CER must observe diacritic change, got=%v",got) }
}

func TestPercentileInterpolates(t *testing.T) {
	if got:=Percentile([]float64{100,200,300,400},.5); got!=250 { t.Fatalf("p50=%v",got) }
	if got:=Percentile([]float64{100,200,300,400},.95); got!=385 { t.Fatalf("p95=%v",got) }
}
