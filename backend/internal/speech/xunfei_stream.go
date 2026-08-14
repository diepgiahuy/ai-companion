package speech

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const xunfeiPCMFrameBytes = 1280

type XunfeiStreamASRConfig struct {
	URL       string
	AppID     string
	APIKey    string
	APISecret string
	Language  string
	Accent    string
	Domain    string
	VADMS     int
	DynamicCorrection bool
	HTTPClient *http.Client
	Now        func() time.Time
}

func (c XunfeiStreamASRConfig) normalized() (XunfeiStreamASRConfig, error) {
	if strings.TrimSpace(c.URL) == "" { c.URL = "wss://iat-api.xfyun.cn/v2/iat" }
	parsed, err := url.Parse(c.URL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" { return c, fmt.Errorf("invalid Xunfei ASR URL %q", c.URL) }
	if parsed.Scheme != "wss" && parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost" { return c, errors.New("Xunfei ASR URL must use wss outside localhost") }
	if strings.TrimSpace(c.AppID) == "" || strings.TrimSpace(c.APIKey) == "" || strings.TrimSpace(c.APISecret) == "" { return c, errors.New("Xunfei ASR app id, API key and API secret are required") }
	if c.Language == "" { c.Language = "zh_cn" }
	if c.Accent == "" { c.Accent = "mandarin" }
	if c.Domain == "" { c.Domain = "iat" }
	if c.VADMS <= 0 { c.VADMS = 1000 }
	if c.Now == nil { c.Now = time.Now }
	return c, nil
}

type XunfeiStreamASRProvider struct { config XunfeiStreamASRConfig }

func NewXunfeiStreamASR(config XunfeiStreamASRConfig) (*XunfeiStreamASRProvider, error) {
	normalized, err := config.normalized(); if err != nil { return nil, err }
	return &XunfeiStreamASRProvider{config: normalized}, nil
}

func (p *XunfeiStreamASRProvider) StartASR(ctx context.Context, request ASRRequest, emit func(TranscriptEvent) error) (ASRStream, error) {
	if p == nil { return nil, errors.New("Xunfei ASR provider is nil") }
	if emit == nil { return nil, errors.New("Xunfei ASR emit callback is required") }
	if err := request.Format.Validate(); err != nil { return nil, err }
	if request.Format.SampleRate != 16000 { return nil, fmt.Errorf("Xunfei streaming ASR requires 16000 Hz PCM; got %d", request.Format.SampleRate) }
	signedURL, err := xunfeiSignedURL(p.config.URL, p.config.APIKey, p.config.APISecret, p.config.Now()); if err != nil { return nil, err }
	options := &websocket.DialOptions{}; if p.config.HTTPClient != nil { options.HTTPClient = p.config.HTTPClient }
	conn, _, err := websocket.Dial(ctx, signedURL, options); if err != nil { return nil, fmt.Errorf("dial Xunfei ASR: %w", err) }
	streamCtx, cancel := context.WithCancel(ctx)
	s := &xunfeiASRStream{conn: conn, cancel: cancel, config: p.config, emit: emit, done: make(chan struct{}), segments: make(map[int]string)}
	go s.readLoop(streamCtx)
	return s, nil
}

func xunfeiSignedURL(rawURL, apiKey, apiSecret string, now time.Time) (string, error) {
	parsed, err := url.Parse(rawURL); if err != nil { return "", err }
	if parsed.Host == "" { return "", errors.New("Xunfei ASR URL host is required") }
	date := strings.Replace(now.UTC().Format(time.RFC1123), "UTC", "GMT", 1)
	requestLine := "GET " + parsed.EscapedPath() + " HTTP/1.1"
	origin := fmt.Sprintf("host: %s\ndate: %s\n%s", parsed.Host, date, requestLine)
	mac := hmac.New(sha256.New, []byte(apiSecret)); _, _ = mac.Write([]byte(origin))
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	authOrigin := fmt.Sprintf(`api_key="%s", algorithm="hmac-sha256", headers="host date request-line", signature="%s"`, apiKey, signature)
	query := parsed.Query(); query.Set("authorization", base64.StdEncoding.EncodeToString([]byte(authOrigin))); query.Set("date", date); query.Set("host", parsed.Host); parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

type xunfeiASRStream struct {
	conn *websocket.Conn
	cancel context.CancelFunc
	config XunfeiStreamASRConfig
	emit func(TranscriptEvent) error
	writeMu sync.Mutex
	mu sync.Mutex
	pending []byte
	started bool
	closedInput bool
	closed bool
	segments map[int]string
	lastText string
	err error
	done chan struct{}
	once sync.Once
}

func (s *xunfeiASRStream) finish(err error) { s.once.Do(func(){ s.mu.Lock(); s.err=err; s.mu.Unlock(); close(s.done) }) }

func (s *xunfeiASRStream) Push(ctx context.Context, pcm []byte) error {
	if len(pcm)==0 { return nil }; if err:=ctx.Err(); err!=nil { return err }
	s.writeMu.Lock(); defer s.writeMu.Unlock(); if s.closedInput { return errors.New("Xunfei ASR input is already closed") }
	s.pending=append(s.pending, pcm...)
	for len(s.pending)>=xunfeiPCMFrameBytes { frame:=append([]byte(nil),s.pending[:xunfeiPCMFrameBytes]...); s.pending=s.pending[xunfeiPCMFrameBytes:]; status:=1; if !s.started { status=0; s.started=true }; if err:=s.writeFrame(ctx,status,frame); err!=nil { return err } }
	return nil
}

func (s *xunfeiASRStream) CloseInput(ctx context.Context) error {
	if err:=ctx.Err(); err!=nil { return err }; s.writeMu.Lock(); defer s.writeMu.Unlock(); if s.closedInput { return nil }
	if len(s.pending)>0 { status:=1; if !s.started { status=0; s.started=true }; if err:=s.writeFrame(ctx,status,append([]byte(nil),s.pending...)); err!=nil { return err }; s.pending=nil }
	if !s.started { if err:=s.writeFrame(ctx,0,nil); err!=nil { return err }; s.started=true }
	if err:=s.writeFrame(ctx,2,nil); err!=nil { return err }; s.closedInput=true; return nil
}

func (s *xunfeiASRStream) writeFrame(ctx context.Context,status int,audio []byte) error {
	payload:=map[string]any{"data":map[string]any{"status":status,"format":"audio/L16;rate=16000","encoding":"raw","audio":base64.StdEncoding.EncodeToString(audio)}}
	if status==0 { business:=map[string]any{"domain":s.config.Domain,"language":s.config.Language,"accent":s.config.Accent,"vinfo":1,"vad_eos":s.config.VADMS,"nunum":1,"ptt":1}; if s.config.DynamicCorrection { business["dwa"]="wpgs" }; payload["common"]=map[string]any{"app_id":s.config.AppID}; payload["business"]=business }
	raw,err:=json.Marshal(payload); if err!=nil { return err }; if err:=s.conn.Write(ctx,websocket.MessageText,raw); err!=nil { return fmt.Errorf("write Xunfei ASR frame status=%d: %w",status,err) }; return nil
}

type xunfeiResponse struct { Code int `json:"code"`; Message string `json:"message"`; SID string `json:"sid"`; Data struct { Status int `json:"status"`; Result struct { SN int `json:"sn"`; PGS string `json:"pgs"`; RG []int `json:"rg"`; WS []struct { CW []struct { W string `json:"w"` } `json:"cw"` } `json:"ws"` } `json:"result"` } `json:"data"` }

func (s *xunfeiASRStream) readLoop(ctx context.Context) {
	for { kind,raw,err:=s.conn.Read(ctx); if err!=nil { if ctx.Err()!=nil { s.finish(ctx.Err()) } else { s.finish(fmt.Errorf("read Xunfei ASR: %w",err)) }; return }; if kind!=websocket.MessageText { continue }; var response xunfeiResponse; if err:=json.Unmarshal(raw,&response); err!=nil { s.finish(fmt.Errorf("decode Xunfei ASR response: %w",err)); return }; if response.Code!=0 { s.finish(fmt.Errorf("Xunfei ASR code=%d: %s",response.Code,response.Message)); return }; piece:=xunfeiWords(response.Data.Result.WS); result:=response.Data.Result; s.mu.Lock(); if result.PGS=="rpl" && len(result.RG)==2 { for i:=result.RG[0]; i<=result.RG[1]; i++ { delete(s.segments,i) } }; if piece!="" { s.segments[result.SN]=piece }; text:=joinXunfeiSegments(s.segments); if text!="" { s.lastText=text }; finalText:=s.lastText; s.mu.Unlock(); final:=response.Data.Status==2; if text!="" { if err:=s.emit(TranscriptEvent{Text:text,Final:final,Stable:final}); err!=nil { s.finish(err); return } } else if final && finalText!="" { if err:=s.emit(TranscriptEvent{Text:finalText,Final:true,Stable:true}); err!=nil { s.finish(err); return } }; if final { s.finish(nil); return } }
}

func xunfeiWords(ws []struct { CW []struct { W string `json:"w"` } `json:"cw"` }) string { var b strings.Builder; for _,group:=range ws { if len(group.CW)>0 { b.WriteString(group.CW[0].W) } }; return b.String() }
func joinXunfeiSegments(segments map[int]string) string { if len(segments)==0 { return "" }; max:=0; for sn:=range segments { if sn>max { max=sn } }; var b strings.Builder; for i:=0;i<=max;i++ { b.WriteString(segments[i]) }; return b.String() }
func (s *xunfeiASRStream) Wait(ctx context.Context)(string,error){ select { case <-ctx.Done(): return "",ctx.Err(); case <-s.done: s.mu.Lock(); defer s.mu.Unlock(); return s.lastText,s.err } }
func (s *xunfeiASRStream) Close() error { s.mu.Lock(); if s.closed { s.mu.Unlock(); return nil }; s.closed=true; s.mu.Unlock(); s.cancel(); return s.conn.Close(websocket.StatusNormalClosure,"companion Xunfei ASR closed") }

var _ StreamingASRProvider = (*XunfeiStreamASRProvider)(nil)
