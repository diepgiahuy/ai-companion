#!/usr/bin/env python3
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
PATH = ROOT / "backend/internal/server/server.go"
text = PATH.read_text(encoding="utf-8")

def replace_once(old: str, new: str) -> None:
    global text
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"expected one guarded server match, found {count}\n--- needle ---\n{old[:500]}")
    text = text.replace(old, new)

replace_once(
    '''\tobserver      observability.Recorder\n\tcontrolWrites chan outbound''',
    '''\tobserver      observability.Recorder\n\tcorrelator    *observability.Correlator\n\tcontrolWrites chan outbound''',
)
replace_once(
    '''\tif observer == nil {\n\t\tobserver = observability.Nop()\n\t}\n\treturn &session{''',
    '''\tif observer == nil {\n\t\tobserver = observability.Nop()\n\t}\n\tcorrelator := observability.NewCorrelator(id)\n\treturn &session{''',
)
replace_once(
    '''\t\tobserver:      observer,\n\t\tcontrolWrites: make(chan outbound, maximumControlQueue),''',
    '''\t\tobserver:      observer,\n\t\tcorrelator:    correlator,\n\t\tcontrolWrites: make(chan outbound, maximumControlQueue),''',
)
replace_once(
    '''\tobservability.RecordTo(s.observer, observability.Event{\n\t\tName: observability.EventSessionEnd, DurationMS: time.Since(sessionStarted).Milliseconds(),\n\t\tOutcome: observabilityOutcome(sessionErr), Correlation: observability.Correlation{SessionID: session.id},\n\t})''',
    '''\tobservability.RecordTo(s.observer, observability.Event{\n\t\tName: observability.EventSessionEnd, DurationMS: time.Since(sessionStarted).Milliseconds(),\n\t\tOutcome: observabilityOutcome(sessionErr), Correlation: session.sessionCorrelation(),\n\t})''',
)
replace_once(
    '''\tobservability.RecordTo(s.observer, observability.Event{\n\t\tName: observability.EventSessionReady, Outcome: "ok",\n\t\tCorrelation: observability.Correlation{SessionID: s.id},\n\t})''',
    '''\tobservability.RecordTo(s.observer, observability.Event{\n\t\tName: observability.EventSessionReady, Outcome: "ok",\n\t\tCorrelation: s.sessionCorrelation(),\n\t})''',
)
replace_once(
    '''\tobservability.RecordTo(s.observer, observability.Event{\n\t\tName: observability.EventTurnStart, Outcome: "ok",\n\t\tCorrelation: observability.Correlation{SessionID: s.id, TurnID: turnID, GenerationID: generation},\n\t})\n\tif interrupted != nil {\n\t\tobservability.RecordTo(s.observer, observability.Event{\n\t\t\tName: observability.EventTurnInterrupted, Outcome: "cancelled", Reason: "barge_in",\n\t\t\tCorrelation: observability.Correlation{SessionID: s.id, TurnID: interrupted.id, GenerationID: interrupted.generation},\n\t\t})''',
    '''\tobservability.RecordTo(s.observer, observability.Event{\n\t\tName: observability.EventTurnStart, Outcome: "ok",\n\t\tCorrelation: s.correlation(turnID, generation),\n\t})\n\tif interrupted != nil {\n\t\tobservability.RecordTo(s.observer, observability.Event{\n\t\t\tName: observability.EventTurnInterrupted, Outcome: "cancelled", Reason: "barge_in",\n\t\t\tCorrelation: s.turnCorrelation(interrupted),\n\t\t})''',
)
replace_once(
    '''func (s *session) turnCorrelation(current *turn) observability.Correlation {\n\tif current == nil {\n\t\treturn observability.Correlation{SessionID: s.id}\n\t}\n\treturn observability.Correlation{SessionID: s.id, TurnID: current.id, GenerationID: current.generation}\n}''',
    '''func (s *session) correlation(turnID string, generation uint64) observability.Correlation {\n\tif s == nil || s.correlator == nil {\n\t\treturn observability.Correlation{GenerationID: generation}\n\t}\n\treturn observability.Correlation{\n\t\tSessionID: s.correlator.Opaque(s.id),\n\t\tTurnID: s.correlator.Opaque(turnID),\n\t\tGenerationID: generation,\n\t}\n}\n\nfunc (s *session) sessionCorrelation() observability.Correlation {\n\treturn s.correlation("", 0)\n}\n\nfunc (s *session) turnCorrelation(current *turn) observability.Correlation {\n\tif current == nil {\n\t\treturn s.sessionCorrelation()\n\t}\n\treturn s.correlation(current.id, current.generation)\n}''',
)
replace_once(
    '''\t\tobservability.RecordTo(s.observer, observability.Event{\n\t\t\tName: observability.EventQueueFull, Outcome: "error", Queue: label, QueueCapacity: cap(queue),\n\t\t\tCorrelation: observability.Correlation{SessionID: s.id, GenerationID: message.generation},\n\t\t})''',
    '''\t\tobservability.RecordTo(s.observer, observability.Event{\n\t\t\tName: observability.EventQueueFull, Outcome: "error", Queue: label, QueueCapacity: cap(queue),\n\t\t\tCorrelation: s.correlation("", message.generation),\n\t\t})''',
)

PATH.write_text(text, encoding="utf-8")
print("issue25 correlation pseudonym patch applied")
