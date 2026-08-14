package speech

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

type edgeRunnerCall struct {
	command string
	args    []string
	stdin   []byte
	limit   int
}

type fakeEdgeRunner struct {
	calls []edgeRunnerCall
	run   func(int, edgeRunnerCall) ([]byte, error)
}

func (r *fakeEdgeRunner) Run(_ context.Context, command string, args []string, stdin []byte, limit int) ([]byte, error) {
	call := edgeRunnerCall{command: command, args: append([]string(nil), args...), stdin: append([]byte(nil), stdin...), limit: limit}
	r.calls = append(r.calls, call)
	if r.run != nil {
		return r.run(len(r.calls)-1, call)
	}
	return nil, nil
}

func TestEdgeTTSUsesUpstreamCLIThenDecodesTo24kPCM(t *testing.T) {
	pcm := make([]byte, 6000)
	for i := range pcm {
		pcm[i] = byte(i % 251)
	}
	runner := &fakeEdgeRunner{}
	runner.run = func(index int, call edgeRunnerCall) ([]byte, error) {
		switch index {
		case 0:
			if call.command != "edge-tts" {
				t.Fatalf("command=%q", call.command)
			}
			joined := strings.Join(call.args, " ")
			for _, required := range []string{"--file -", "--voice vi-VN-HoaiMyNeural", "--write-media -", "--rate=+0%", "--volume=+0%", "--pitch=+0Hz"} {
				if !strings.Contains(joined, required) {
					t.Fatalf("edge args=%q missing %q", joined, required)
				}
			}
			if string(call.stdin) != "xin chào" {
				t.Fatalf("edge stdin=%q", call.stdin)
			}
			return []byte("fake-mp3"), nil
		case 1:
			if call.command != "ffmpeg" {
				t.Fatalf("command=%q", call.command)
			}
			if string(call.stdin) != "fake-mp3" {
				t.Fatalf("ffmpeg stdin=%q", call.stdin)
			}
			joined := strings.Join(call.args, " ")
			for _, required := range []string{"-f s16le", "-ac 1", "-ar 24000", "pipe:1"} {
				if !strings.Contains(joined, required) {
					t.Fatalf("ffmpeg args=%q missing %q", joined, required)
				}
			}
			return pcm, nil
		default:
			t.Fatalf("unexpected runner call %d", index)
			return nil, nil
		}
	}
	provider, err := NewEdgeTTS(EdgeTTSConfig{Runner: runner, PCMChunkBytes: 2880})
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	final := false
	err = provider.Synthesize(context.Background(), TTSRequest{
		Text: "xin chào", Voice: "vi-VN-HoaiMyNeural", Locale: "vi-VN",
		Format: AudioFormat{SampleRate: 24000, Channels: 1},
	}, func(event AudioEvent) error {
		if len(event.PCM) > 0 {
			got.Write(event.PCM)
		}
		if event.Final {
			final = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), pcm) || !final {
		t.Fatalf("pcm=%d bytes final=%v", got.Len(), final)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("runner calls=%d", len(runner.calls))
	}
}

func TestEdgeTTSFailsClosedOnSynthesisOrDecodeFailure(t *testing.T) {
	t.Run("synthesis", func(t *testing.T) {
		runner := &fakeEdgeRunner{run: func(_ int, _ edgeRunnerCall) ([]byte, error) { return nil, errors.New("edge unavailable") }}
		provider, err := NewEdgeTTS(EdgeTTSConfig{Runner: runner})
		if err != nil { t.Fatal(err) }
		err = provider.Synthesize(context.Background(), TTSRequest{Text: "hello", Format: AudioFormat{SampleRate: 24000, Channels: 1}}, func(AudioEvent) error { return nil })
		if err == nil || !strings.Contains(err.Error(), "synthesize MP3") { t.Fatalf("error=%v", err) }
	})

	t.Run("decode", func(t *testing.T) {
		runner := &fakeEdgeRunner{run: func(index int, _ edgeRunnerCall) ([]byte, error) {
			if index == 0 { return []byte("mp3"), nil }
			return nil, errors.New("bad mp3")
		}}
		provider, err := NewEdgeTTS(EdgeTTSConfig{Runner: runner})
		if err != nil { t.Fatal(err) }
		err = provider.Synthesize(context.Background(), TTSRequest{Text: "hello", Format: AudioFormat{SampleRate: 24000, Channels: 1}}, func(AudioEvent) error { return nil })
		if err == nil || !strings.Contains(err.Error(), "decode EdgeTTS MP3") { t.Fatalf("error=%v", err) }
	})
}

func TestEdgeTTSRejectsWrongFormatAndOddChunkSize(t *testing.T) {
	if _, err := NewEdgeTTS(EdgeTTSConfig{PCMChunkBytes: 3}); err == nil {
		t.Fatal("odd PCM chunk size should fail")
	}
	provider, err := NewEdgeTTS(EdgeTTSConfig{Runner: &fakeEdgeRunner{}})
	if err != nil { t.Fatal(err) }
	err = provider.Synthesize(context.Background(), TTSRequest{Text: "hello", Format: AudioFormat{SampleRate: 16000, Channels: 1}}, func(AudioEvent) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "24000 Hz mono") { t.Fatalf("error=%v", err) }
}

func TestLimitedBufferFailsWhenOutputExceedsLimit(t *testing.T) {
	var out bytes.Buffer
	writer := &limitedBuffer{buffer: &out, remaining: 4}
	if _, err := writer.Write([]byte{1, 2, 3, 4, 5}); err == nil {
		t.Fatal("overflow should fail")
	}
	if out.Len() != 4 || !writer.overflow {
		t.Fatalf("len=%d overflow=%v", out.Len(), writer.overflow)
	}
}
