package protocol

import "testing"

func TestValidateHello(t *testing.T) {
	audio := DefaultAudioParams()
	valid := Message{Type: "hello", Version: Version, Transport: Transport, AudioParams: &audio}
	if err := ValidateHello(valid); err != nil {
		t.Fatalf("valid hello rejected: %v", err)
	}
	invalid := valid
	invalid.AudioParams = &AudioParams{Format: "pcm_s16le", SampleRate: 16000, Channels: 1, FrameDurationMS: 20}
	if err := ValidateHello(invalid); err == nil {
		t.Fatal("incompatible audio params were accepted")
	}
}
