package speech

import (
	"regexp"
	"testing"
)

func TestQwenRealtimeToolAliasIsProviderSafeAndStable(t *testing.T) {
	first := qwenRealtimeToolAlias("expense.log")
	second := qwenRealtimeToolAlias("expense.log")
	other := qwenRealtimeToolAlias("expense-log")
	if first != second { t.Fatalf("alias not stable: %q != %q", first, second) }
	if first == other { t.Fatalf("distinct canonical tools collided: %q", first) }
	if ok,_ := regexp.MatchString(`^[A-Za-z0-9_-]+$`, first); !ok { t.Fatalf("unsafe alias=%q", first) }
}

func TestQwenRealtimeToolsPreserveCanonicalMapping(t *testing.T) {
	aliases, tools, err := qwenRealtimeTools([]NativeRealtimeTool{{Name:"memory.remember",Description:"save memory",Parameters:map[string]any{"type":"object"}}})
	if err != nil { t.Fatal(err) }
	if len(tools)!=1 || len(aliases)!=1 { t.Fatalf("tools=%d aliases=%d", len(tools),len(aliases)) }
	for alias, canonical := range aliases { if canonical!="memory.remember" || alias==canonical { t.Fatalf("mapping=%q -> %q",alias,canonical) } }
}

func TestQwenRealtimeConfigFailsClosed(t *testing.T) {
	if _,err:=NewQwenRealtime(QwenRealtimeConfig{URL:"ws://example.com/realtime",Model:"m",APIKey:"k"});err==nil{t.Fatal("plaintext remote websocket must fail")}
	if _,err:=NewQwenRealtime(QwenRealtimeConfig{URL:"wss://example.com/realtime",Model:"m"});err==nil{t.Fatal("missing API key must fail")}
}
