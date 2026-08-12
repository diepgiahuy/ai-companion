package realtime

import "testing"

func TestTurnDetectorWaitsLongerForShortUnstableUtterance(t *testing.T) {
	d := NewTurnDetector(DefaultTurnDetectorConfig())
	decision := d.Decide(TurnSignal{PartialText: "ừ", SpeechDurationMS: 500, SilenceMS: 900})
	if decision.Finalize {
		t.Fatalf("short unstable utterance finalized too early: %+v", decision)
	}
	if decision.RequiredSilenceMS <= 800 {
		t.Fatalf("short utterance should require extra silence: %+v", decision)
	}
}

func TestTurnDetectorFinalizesStableTerminalSentenceEarlier(t *testing.T) {
	d := NewTurnDetector(DefaultTurnDetectorConfig())
	decision := d.Decide(TurnSignal{
		PartialText:      "Hôm nay tôi muốn xem tổng chi tiêu trong tháng này.",
		Stable:           true,
		SpeechDurationMS: 1800,
		SilenceMS:        400,
	})
	if !decision.Finalize {
		t.Fatalf("stable terminal sentence should finalize with shorter silence: %+v", decision)
	}
}

func TestTurnDetectorDoesNotFinalizeBeforeMinimumSpeech(t *testing.T) {
	d := NewTurnDetector(DefaultTurnDetectorConfig())
	decision := d.Decide(TurnSignal{PartialText: "đặt timer", Stable: true, SpeechDurationMS: 100, SilenceMS: 2000})
	if decision.Finalize || decision.Reason != "minimum_speech_not_met" {
		t.Fatalf("minimum speech invariant failed: %+v", decision)
	}
}

func TestTurnDetectorNoTranscriptUsesMaximumSilence(t *testing.T) {
	cfg := DefaultTurnDetectorConfig()
	d := NewTurnDetector(cfg)
	decision := d.Decide(TurnSignal{SpeechDurationMS: 1000, SilenceMS: cfg.BaseSilenceMS})
	if decision.Finalize || decision.RequiredSilenceMS != cfg.MaxSilenceMS {
		t.Fatalf("missing transcript should be conservative: %+v", decision)
	}
}
