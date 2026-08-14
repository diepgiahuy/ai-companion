package speech

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
)

const (
	edgeTrustedClientToken = "6A5AA1D4EAFF4E9FB37E23D68491D6F4"
	edgeChromiumVersion    = "143.0.3650.75"
	edgeBaseWSS            = "wss://speech.platform.bing.com/consumer/speech/synthesize/readaloud/edge/v1"
	edgeRawPCM24k          = "raw-24khz-16bit-mono-pcm"
)

type EdgeTTSConfig struct {
	URL                string
	Voice              string
	TrustedClientToken string
	ChromiumVersion    string
	Rate               string
	Volume             string
	Pitch              string
	HTTPClient         *http.Client
	Now                func() time.Time
}

func (c EdgeTTSConfig) normalized() (EdgeTTSConfig, error) {
	if strings.TrimSpace(c.URL) == "" { c.URL = edgeBaseWSS }
	parsed, err := url.Parse(c.URL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return c, fmt.Errorf("invalid EdgeTTS URL %q", c.URL)
	}
	if parsed.Scheme != "wss" && parsed.Hostname() != "localhost" && parsed.Hostname() != "127.0.0.1" {
		return c, errors.New("EdgeTTS URL must use wss outside localhost")
	}
	if strings.TrimSpace(c.Voice) == "" { c.Voice = "en-US-EmmaMultilingualNeural" }
	if strings.TrimSpace(c.TrustedClientToken) == "" { c.TrustedClientToken = edgeTrustedClientToken }
	if strings.TrimSpace(c.ChromiumVersion) == "" { c.ChromiumVersion = edgeChromiumVersion }
	if c.Rate == "" { c.Rate = "+0%" }
	if c.Volume == "" { c.Volume = "+0%" }
	if c.Pitch == "" { c.Pitch = "+0Hz" }
	if c.Now == nil { c.Now = time.Now }
	return c, nil
}

type EdgeTTSProvider struct { config EdgeTTSConfig }

func NewEdgeTTS(config EdgeTTSConfig) (*EdgeTTSProvider, error) {
	normalized, err := config.normalized()
	if err != nil { return nil, err }
	return &EdgeTTSProvider{config: normalized}, nil
}

func (p *EdgeTTSProvider) Synthesize(ctx context.Context, request TTSRequest, emit func(AudioEvent) error) error {
	if p == nil { return errors.New("EdgeTTS provider is nil") }
	if emit == nil { return errors.New("EdgeTTS emit callback is required") }
	if err := request.Format.Validate(); err != nil { return err }
	if request.Format.SampleRate != 24000 {
		return fmt.Errorf("EdgeTTS reference path requires 24000 Hz PCM; got %d", request.Format.SampleRate)
	}
	if strings.TrimSpace(request.Text) == "" { return errors.New("EdgeTTS text is required") }

	requestID, err := randomHex(16)
	if err != nil { return err }
	muid, err := randomHex(16)
	if err != nil { return err }
	dialURL, err := edgeDialURL(p.config, requestID)
	if err != nil { return err }

	headers := http.Header{}
	major := strings.SplitN(p.config.ChromiumVersion, ".", 2)[0]
	headers.Set("User-Agent", fmt.Sprintf("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s.0.0.0 Safari/537.36 Edg/%s.0.0.0", major, major))
	headers.Set("Pragma", "no-cache")
	headers.Set("Cache-Control", "no-cache")
	headers.Set("Origin", "chrome-extension://jdiccldimpdaibmpdkjnbmckianbfold")
	headers.Set("Cookie", "muid="+strings.ToUpper(muid)+";")
	options := &websocket.DialOptions{HTTPHeader: headers}
	if p.config.HTTPClient != nil { options.HTTPClient = p.config.HTTPClient }
	conn, _, err := websocket.Dial(ctx, dialURL, options)
	if err != nil { return fmt.Errorf("dial EdgeTTS: %w", err) }
	defer conn.Close(websocket.StatusNormalClosure, "EdgeTTS synthesis complete")

	voice := p.config.Voice
	if strings.TrimSpace(request.Voice) != "" { voice = request.Voice }
	timestamp := p.config.Now().UTC().Format(time.RFC1123)
	timestamp = strings.Replace(timestamp, "UTC", "GMT", 1)
	configMessage := fmt.Sprintf("X-Timestamp:%s\r\nContent-Type:application/json; charset=utf-8\r\nPath:speech.config\r\n\r\n{\"context\":{\"synthesis\":{\"audio\":{\"metadataoptions\":{\"sentenceBoundaryEnabled\":\"false\",\"wordBoundaryEnabled\":\"false\"},\"outputFormat\":\"%s\"}}}}\r\n", timestamp, edgeRawPCM24k)
	if err := conn.Write(ctx, websocket.MessageText, []byte(configMessage)); err != nil {
		return fmt.Errorf("write EdgeTTS speech config: %w", err)
	}
	ssml := edgeSSML(request.Text, voice, p.config.Rate, p.config.Volume, p.config.Pitch)
	ssmlMessage := fmt.Sprintf("X-RequestId:%s\r\nContent-Type:application/ssml+xml\r\nX-Timestamp:%s\r\nPath:ssml\r\n\r\n%s", requestID, timestamp, ssml)
	if err := conn.Write(ctx, websocket.MessageText, []byte(ssmlMessage)); err != nil {
		return fmt.Errorf("write EdgeTTS SSML: %w", err)
	}

	receivedAudio := false
	for {
		kind, raw, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil { return ctx.Err() }
			return fmt.Errorf("read EdgeTTS response: %w", err)
		}
		switch kind {
		case websocket.MessageBinary:
			pcm, ok := edgeAudioPayload(raw)
			if !ok || len(pcm) == 0 { continue }
			receivedAudio = true
			if err := emit(AudioEvent{PCM: pcm}); err != nil { return err }
		case websocket.MessageText:
			message := string(raw)
			if strings.Contains(message, "Path:turn.end") {
				if !receivedAudio { return errors.New("EdgeTTS completed without audio") }
				return emit(AudioEvent{Final: true})
			}
		}
	}
}

func edgeDialURL(config EdgeTTSConfig, connectionID string) (string, error) {
	parsed, err := url.Parse(config.URL)
	if err != nil { return "", err }
	query := parsed.Query()
	query.Set("TrustedClientToken", config.TrustedClientToken)
	query.Set("Sec-MS-GEC", edgeSecMSGEC(config.Now(), config.TrustedClientToken))
	query.Set("Sec-MS-GEC-Version", "1-"+config.ChromiumVersion)
	query.Set("ConnectionId", connectionID)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func edgeSecMSGEC(now time.Time, trustedClientToken string) string {
	seconds := now.UTC().Unix() + 11644473600
	seconds -= seconds % 300
	windowsTicks := seconds * 10_000_000
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d%s", windowsTicks, trustedClientToken)))
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

func edgeSSML(text, voice, rate, volume, pitch string) string {
	return fmt.Sprintf(`<speak version="1.0" xmlns="http://www.w3.org/2001/10/synthesis" xml:lang="en-US"><voice name="%s"><prosody pitch="%s" rate="%s" volume="%s">%s</prosody></voice></speak>`,
		html.EscapeString(voice), html.EscapeString(pitch), html.EscapeString(rate), html.EscapeString(volume), html.EscapeString(text))
}

func edgeAudioPayload(raw []byte) ([]byte, bool) {
	marker := []byte("Path:audio\r\n")
	index := strings.Index(string(raw), string(marker))
	if index < 0 { return nil, false }
	start := index + len(marker)
	if start >= len(raw) { return nil, false }
	return append([]byte(nil), raw[start:]...), true
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil { return "", err }
	return hex.EncodeToString(buf), nil
}

var _ StreamingTTSProvider = (*EdgeTTSProvider)(nil)
