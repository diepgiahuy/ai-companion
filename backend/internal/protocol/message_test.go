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

func TestValidateUIState(t *testing.T) {
	valid := Message{Type: "ui_state", Emotion: UIEmotionToolExecuting, ToolName: "expense.log"}
	if err := ValidateUIState(valid); err != nil {
		t.Fatalf("valid ui state rejected: %v", err)
	}
	if err := ValidateUIState(Message{Type: "ui_state", Emotion: UIEmotionToolExecuting}); err == nil {
		t.Fatal("tool executing state without tool_name was accepted")
	}
	if err := ValidateUIState(Message{Type: "ui_state", Emotion: "dancing"}); err == nil {
		t.Fatal("unknown emotion was accepted")
	}
	if err := ValidateUIState(Message{Type: "ui_state", Emotion: UIEmotionSpeaking, ToolName: "expense.log"}); err == nil {
		t.Fatal("tool_name leaked into a non-tool state")
	}
}
