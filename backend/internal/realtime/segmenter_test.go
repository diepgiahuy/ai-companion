package realtime

import (
	"reflect"
	"testing"
)

func TestSegmenterStreamsFirstClauseEarly(t *testing.T) {
	s := NewSegmenter()
	var got []string
	for _, delta := range []string{"Xin", " chào", " bạn,", " hôm", " nay", " thế", " nào?"} {
		got = append(got, s.Push(delta)...)
	}
	got = append(got, s.Flush()...)
	want := []string{"Xin chào bạn,", "hôm nay thế nào?"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("segments=%q want=%q", got, want)
	}
}

func TestSegmenterHandlesVietnamesePunctuation(t *testing.T) {
	s := NewSegmenter()
	got := s.Push("Mình đã lưu khoản chi này. Bạn còn 750 nghìn trong ngân sách tuần!")
	got = append(got, s.Flush()...)
	want := []string{"Mình đã lưu khoản chi này.", "Bạn còn 750 nghìn trong ngân sách tuần!"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("segments=%q want=%q", got, want)
	}
}

func TestSegmenterFlushesTail(t *testing.T) {
	s := NewSegmenter()
	if got := s.Push("Đã ghi chú"); len(got) != 0 {
		t.Fatalf("unexpected early segments=%q", got)
	}
	if got := s.Flush(); !reflect.DeepEqual(got, []string{"Đã ghi chú"}) {
		t.Fatalf("flush=%q", got)
	}
}
