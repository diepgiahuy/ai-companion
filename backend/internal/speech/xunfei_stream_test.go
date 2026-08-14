package speech

import (
	"encoding/base64"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestXunfeiSignedURLIsDeterministicAndKeepsHost(t *testing.T) {
	now := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	raw, err := xunfeiSignedURL("wss://iat-api.xfyun.cn/v2/iat", "key", "secret", now)
	if err != nil { t.Fatal(err) }
	parsed, err := url.Parse(raw); if err != nil { t.Fatal(err) }
	if parsed.Scheme != "wss" || parsed.Host != "iat-api.xfyun.cn" { t.Fatalf("url=%s", raw) }
	q := parsed.Query()
	for _, key := range []string{"authorization", "date", "host"} { if q.Get(key)=="" { t.Fatalf("missing %s", key) } }
	decoded, err := base64.StdEncoding.DecodeString(q.Get("authorization")); if err != nil { t.Fatal(err) }
	if !strings.Contains(string(decoded), `api_key="key"`) || !strings.Contains(string(decoded), "hmac-sha256") { t.Fatalf("authorization=%q", decoded) }
}

func TestXunfeiConfigFailsClosed(t *testing.T) {
	if _, err := NewXunfeiStreamASR(XunfeiStreamASRConfig{URL:"ws://example.com/v2/iat",AppID:"a",APIKey:"k",APISecret:"s"}); err == nil { t.Fatal("plaintext remote websocket must fail") }
	if _, err := NewXunfeiStreamASR(XunfeiStreamASRConfig{URL:"wss://example.com/v2/iat"}); err == nil { t.Fatal("missing credentials must fail") }
}

func TestJoinXunfeiSegmentsOrdersCorrections(t *testing.T) {
	got := joinXunfeiSegments(map[int]string{2:"bạn",0:"xin ",1:"chào "})
	if got != "xin chào bạn" { t.Fatalf("got=%q", got) }
}
