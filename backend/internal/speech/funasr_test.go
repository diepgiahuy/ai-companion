package speech

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFunASRPostsBoundedWAVToOpenAICompatibleEndpoint(t *testing.T) {
	pcm := []byte{1, 0, 2, 0, 3, 0, 4, 0}
	var gotModel, gotLanguage string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" {
			t.Fatalf("path=%q", r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		gotModel = r.FormValue("model")
		gotLanguage = r.FormValue("language")
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		wav, err := io.ReadAll(file)
		if err != nil {
			t.Fatal(err)
		}
		if len(wav) != 44+len(pcm) || string(wav[:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
			t.Fatalf("invalid WAV header len=%d", len(wav))
		}
		if rate := binary.LittleEndian.Uint32(wav[24:28]); rate != 16000 {
			t.Fatalf("sample rate=%d", rate)
		}
		if channels := binary.LittleEndian.Uint16(wav[22:24]); channels != 1 {
			t.Fatalf("channels=%d", channels)
		}
		if !bytes.Equal(wav[44:], pcm) {
			t.Fatalf("pcm=%v", wav[44:])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"text":" xin chào Companion "}`)
	}))
	defer server.Close()

	provider, err := NewFunASR(FunASRConfig{
		BaseURL: server.URL,
		Model: "custom",
		Language: "越南语",
	})
	if err != nil {
		t.Fatal(err)
	}
	var events []TranscriptEvent
	stream, err := provider.StartASR(context.Background(), ASRRequest{
		Format: AudioFormat{SampleRate: 16000, Channels: 1}, Locale: "vi-VN",
	}, func(event TranscriptEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if err := stream.Push(context.Background(), pcm[:4]); err != nil {
		t.Fatal(err)
	}
	if err := stream.Push(context.Background(), pcm[4:]); err != nil {
		t.Fatal(err)
	}
	if err := stream.CloseInput(context.Background()); err != nil {
		t.Fatal(err)
	}
	text, err := stream.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if text != "xin chào Companion" {
		t.Fatalf("text=%q", text)
	}
	if gotModel != "custom" || gotLanguage != "越南语" {
		t.Fatalf("model=%q language=%q", gotModel, gotLanguage)
	}
	if len(events) != 1 || !events[0].Final || !events[0].Stable || events[0].Text != text {
		t.Fatalf("events=%+v", events)
	}
}

func TestFunASRBoundsBufferedTurnAudio(t *testing.T) {
	provider, err := NewFunASR(FunASRConfig{BaseURL: "http://127.0.0.1:1", Model: "custom", MaxPCMBytes: 4})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := provider.StartASR(context.Background(), ASRRequest{Format: AudioFormat{SampleRate: 16000, Channels: 1}}, func(TranscriptEvent) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if err := stream.Push(context.Background(), []byte{1, 0, 2, 0}); err != nil {
		t.Fatal(err)
	}
	if err := stream.Push(context.Background(), []byte{3, 0}); err == nil || !strings.Contains(err.Error(), "turn limit") {
		t.Fatalf("overflow error=%v", err)
	}
}

func TestFunASRHTTPCallHonorsCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	provider, err := NewFunASR(FunASRConfig{BaseURL: server.URL + "/v1", Model: "custom"})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := provider.StartASR(context.Background(), ASRRequest{Format: AudioFormat{SampleRate: 16000, Channels: 1}}, func(TranscriptEvent) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if err := stream.Push(context.Background(), []byte{0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := stream.CloseInput(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = stream.Wait(ctx)
	if err == nil || (!strings.Contains(err.Error(), "context deadline") && !strings.Contains(err.Error(), "context canceled")) {
		t.Fatalf("cancel error=%v", err)
	}
}

func TestFunASRRejectsWrongSampleRateOddPCMAndInvalidConfig(t *testing.T) {
	if _, err := NewFunASR(FunASRConfig{BaseURL: "http://localhost:8000"}); err == nil {
		t.Fatal("missing model should fail")
	}
	provider, err := NewFunASR(FunASRConfig{BaseURL: "http://localhost:8000", Model: "custom"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.StartASR(context.Background(), ASRRequest{Format: AudioFormat{SampleRate: 24000, Channels: 1}}, func(TranscriptEvent) error { return nil }); err == nil || !strings.Contains(err.Error(), "16000 Hz") {
		t.Fatalf("wrong-rate error=%v", err)
	}
	stream, err := provider.StartASR(context.Background(), ASRRequest{Format: AudioFormat{SampleRate: 16000, Channels: 1}}, func(TranscriptEvent) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Push(context.Background(), []byte{1}); err == nil {
		t.Fatal("odd PCM16 input should fail")
	}
}
