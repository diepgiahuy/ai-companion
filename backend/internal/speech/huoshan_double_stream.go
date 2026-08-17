package speech

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
)

const (
	huoshanProtocolVersion = 1
	huoshanHeaderSize = 1
	huoshanFullClientRequest = 0x1
	huoshanFullServerResponse = 0x9
	huoshanAudioOnlyResponse = 0xB
	huoshanErrorInformation = 0xF
	huoshanFlagWithEvent = 0x4
	huoshanJSON = 0x1
	huoshanEventStartSession = 100
	huoshanEventCancelSession = 101
	huoshanEventFinishSession = 102
	huoshanEventSessionStarted = 150
	huoshanEventSessionCanceled = 151
	huoshanEventSessionFinished = 152
	huoshanEventSessionFailed = 153
	huoshanEventTaskRequest = 200
	huoshanEventTTSResponse = 352
)

type HuoshanDoubleStreamTTSConfig struct {
	URL string
	AppID string
	AccessToken string
	ResourceID string
	Speaker string
	AudioParams map[string]any
	Additions map[string]any
	MixSpeaker map[string]any
	HTTPClient *http.Client
}

func (c HuoshanDoubleStreamTTSConfig) normalized() (HuoshanDoubleStreamTTSConfig, error) {
	c.URL = strings.TrimSpace(c.URL)
	if c.URL == "" { return c, errors.New("Huoshan double-stream TTS URL is required") }
	parsed, err := url.Parse(c.URL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" { return c, fmt.Errorf("invalid Huoshan TTS URL %q", c.URL) }
	if parsed.Scheme != "wss" && parsed.Hostname() != "localhost" && parsed.Hostname() != "127.0.0.1" { return c, errors.New("Huoshan TTS URL must use wss outside localhost") }
	if strings.TrimSpace(c.AppID) == "" || strings.TrimSpace(c.AccessToken) == "" || strings.TrimSpace(c.ResourceID) == "" { return c, errors.New("Huoshan TTS app id, access token and resource id are required") }
	if strings.TrimSpace(c.Speaker) == "" { return c, errors.New("Huoshan TTS speaker is required") }
	if c.AudioParams == nil { c.AudioParams = map[string]any{"speech_rate":0,"loudness_rate":0} }
	if c.Additions == nil { c.Additions = map[string]any{"aigc_metadata":map[string]any{},"cache_config":map[string]any{},"post_process":map[string]any{"pitch":0}} }
	return c,nil
}

type HuoshanDoubleStreamTTSProvider struct { config HuoshanDoubleStreamTTSConfig }
func NewHuoshanDoubleStreamTTS(config HuoshanDoubleStreamTTSConfig)(*HuoshanDoubleStreamTTSProvider,error){ normalized,err:=config.normalized(); if err!=nil{return nil,err}; return &HuoshanDoubleStreamTTSProvider{config:normalized},nil }

func (p *HuoshanDoubleStreamTTSProvider) Synthesize(ctx context.Context, request TTSRequest, emit func(AudioEvent) error) error {
	if p == nil {
		return errors.New("Huoshan TTS provider is nil")
	}
	if emit == nil {
		return errors.New("Huoshan TTS emit callback is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := request.Format.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(request.Text) == "" {
		return errors.New("Huoshan TTS text is required")
	}
	connectID, err := huoshanRandomID()
	if err != nil {
		return err
	}
	sessionID, err := huoshanRandomID()
	if err != nil {
		return err
	}
	headers := http.Header{}
	headers.Set("X-Api-App-Key", p.config.AppID)
	headers.Set("X-Api-Access-Key", p.config.AccessToken)
	headers.Set("X-Api-Resource-Id", p.config.ResourceID)
	headers.Set("X-Api-Connect-Id", connectID)
	options := &websocket.DialOptions{HTTPHeader: headers}
	if p.config.HTTPClient != nil {
		options.HTTPClient = p.config.HTTPClient
	}
	conn, _, err := websocket.Dial(ctx, p.config.URL, options)
	if err != nil {
		return fmt.Errorf("dial Huoshan TTS: %w", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "Huoshan synthesis complete")
	defer func() {
		if ctx.Err() == nil {
			return
		}
		cancelCtx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		defer cancel()
		_ = huoshanWriteEvent(cancelCtx, conn, huoshanEventCancelSession, sessionID, []byte(`{}`))
	}()
	voice := p.config.Speaker
	if strings.TrimSpace(request.Voice) != "" {
		voice = strings.TrimSpace(request.Voice)
	}
	startPayload, err := p.payload(huoshanEventStartSession, voice, "", request.Format.SampleRate)
	if err != nil {
		return err
	}
	if err := huoshanWriteEvent(ctx, conn, huoshanEventStartSession, sessionID, startPayload); err != nil {
		return fmt.Errorf("start Huoshan TTS session: %w", err)
	}
	taskPayload, err := p.payload(huoshanEventTaskRequest, voice, request.Text, request.Format.SampleRate)
	if err != nil {
		return err
	}
	if err := huoshanWriteEvent(ctx, conn, huoshanEventTaskRequest, sessionID, taskPayload); err != nil {
		return fmt.Errorf("send Huoshan TTS text: %w", err)
	}
	if err := huoshanWriteEvent(ctx, conn, huoshanEventFinishSession, sessionID, []byte(`{}`)); err != nil {
		return fmt.Errorf("finish Huoshan TTS input: %w", err)
	}
	receivedAudio := false
	for {
		kind, raw, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("read Huoshan TTS response: %w", err)
		}
		if kind != websocket.MessageBinary {
			return errors.New("Huoshan TTS returned non-binary protocol frame")
		}
		response, err := parseHuoshanResponse(raw)
		if err != nil {
			return err
		}
		if response.ErrorCode != 0 || response.MessageType == huoshanErrorInformation {
			return fmt.Errorf("Huoshan TTS error code=%d: %s", response.ErrorCode, strings.TrimSpace(string(response.Payload)))
		}
		switch response.Event {
		case huoshanEventSessionFailed:
			return fmt.Errorf("Huoshan TTS session failed: %s", strings.TrimSpace(string(response.Payload)))
		case huoshanEventTTSResponse:
			if response.MessageType != huoshanAudioOnlyResponse {
				return fmt.Errorf("Huoshan TTS audio event used message type %#x", response.MessageType)
			}
			if len(response.Payload) == 0 {
				continue
			}
			if len(response.Payload)%2 != 0 {
				return fmt.Errorf("Huoshan TTS returned invalid PCM length %d", len(response.Payload))
			}
			receivedAudio = true
			if err := emit(AudioEvent{PCM: append([]byte(nil), response.Payload...)}); err != nil {
				return err
			}
		case huoshanEventSessionFinished:
			if !receivedAudio {
				return errors.New("Huoshan TTS session finished without audio")
			}
			return emit(AudioEvent{Final: true})
		case huoshanEventSessionCanceled:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("Huoshan TTS session canceled by provider")
		}
	}
}

func (p *HuoshanDoubleStreamTTSProvider) payload(event int,speaker,text string,sampleRate int)([]byte,error){
	audioParams:=make(map[string]any,len(p.config.AudioParams)+2); for k,v:=range p.config.AudioParams{audioParams[k]=v}; audioParams["format"]="pcm"; audioParams["sample_rate"]=sampleRate
	additions,err:=json.Marshal(p.config.Additions); if err!=nil{return nil,fmt.Errorf("marshal Huoshan TTS additions: %w",err)}
	reqParams:=map[string]any{"text":text,"speaker":speaker,"audio_params":audioParams,"additions":string(additions)}; if len(p.config.MixSpeaker)>0{reqParams["mix_speaker"]=p.config.MixSpeaker}
	payload:=map[string]any{"user":map[string]any{"uid":"companion"},"event":event,"namespace":"BidirectionalTTS","req_params":reqParams}; encoded,err:=json.Marshal(payload); if err!=nil{return nil,fmt.Errorf("marshal Huoshan TTS request: %w",err)}; return encoded,nil
}

func huoshanWriteEvent(ctx context.Context,conn *websocket.Conn,event int,sessionID string,payload []byte)error{packet,err:=buildHuoshanClientPacket(event,sessionID,payload); if err!=nil{return err}; return conn.Write(ctx,websocket.MessageBinary,packet)}
func buildHuoshanClientPacket(event int,sessionID string,payload []byte)([]byte,error){ if event<=0{return nil,errors.New("Huoshan event must be positive")}; if len(payload)>int(^uint32(0)>>1){return nil,errors.New("Huoshan payload too large")}; packet:=make([]byte,0,16+len(sessionID)+len(payload)); packet=append(packet,byte((huoshanProtocolVersion<<4)|huoshanHeaderSize),byte((huoshanFullClientRequest<<4)|huoshanFlagWithEvent),byte(huoshanJSON<<4),0); packet=appendHuoshanInt32(packet,int32(event)); if sessionID!=""{packet=appendHuoshanContent(packet,[]byte(sessionID))}; packet=appendHuoshanContent(packet,payload); return packet,nil }

type huoshanResponse struct{MessageType int; Event int; SessionID string; Payload []byte; ErrorCode int}
func parseHuoshanResponse(raw []byte)(huoshanResponse,error){
	if len(raw)<4{return huoshanResponse{},errors.New("Huoshan response shorter than header")}; version:=int(raw[0]>>4); headerWords:=int(raw[0]&0x0F); if version!=huoshanProtocolVersion||headerWords<huoshanHeaderSize{return huoshanResponse{},fmt.Errorf("unsupported Huoshan protocol header version=%d size=%d",version,headerWords)}; offset:=headerWords*4; if offset>len(raw){return huoshanResponse{},errors.New("Huoshan response header exceeds frame")}; messageType:=int(raw[1]>>4); flags:=int(raw[1]&0x0F); response:=huoshanResponse{MessageType:messageType}
	if messageType==huoshanErrorInformation{code,next,err:=readHuoshanInt32(raw,offset); if err!=nil{return response,err}; response.ErrorCode=int(code); payload,_,err:=readHuoshanContent(raw,next); if err!=nil{return response,err}; response.Payload=payload; return response,nil}
	if messageType!=huoshanFullServerResponse&&messageType!=huoshanAudioOnlyResponse{return response,fmt.Errorf("unsupported Huoshan response message type %#x",messageType)}; if flags!=huoshanFlagWithEvent{return response,fmt.Errorf("Huoshan response missing event flag: %#x",flags)}; event,next,err:=readHuoshanInt32(raw,offset); if err!=nil{return response,err}; response.Event=int(event); offset=next
	switch response.Event{case 50:_,_,err:=readHuoshanContent(raw,offset);return response,err;case 51:metadata,_,err:=readHuoshanContent(raw,offset);response.Payload=metadata;return response,err;case huoshanEventSessionStarted,huoshanEventSessionFailed,huoshanEventSessionFinished:session,next,err:=readHuoshanContent(raw,offset);if err!=nil{return response,err};response.SessionID=string(session);metadata,_,err:=readHuoshanContent(raw,next);if err!=nil{return response,err};response.Payload=metadata;return response,nil;default:session,next,err:=readHuoshanContent(raw,offset);if err!=nil{return response,err};response.SessionID=string(session);payload,_,err:=readHuoshanContent(raw,next);if err!=nil{return response,err};response.Payload=payload;return response,nil}
}
func appendHuoshanInt32(dst []byte,value int32)[]byte{var encoded [4]byte;binary.BigEndian.PutUint32(encoded[:],uint32(value));return append(dst,encoded[:]...)}
func appendHuoshanContent(dst,content []byte)[]byte{dst=appendHuoshanInt32(dst,int32(len(content)));return append(dst,content...)}
func readHuoshanInt32(raw []byte,offset int)(int32,int,error){if offset<0||offset+4>len(raw){return 0,offset,errors.New("truncated Huoshan int32")};return int32(binary.BigEndian.Uint32(raw[offset:offset+4])),offset+4,nil}
func readHuoshanContent(raw []byte,offset int)([]byte,int,error){size,next,err:=readHuoshanInt32(raw,offset);if err!=nil{return nil,offset,err};if size<0||int64(next)+int64(size)>int64(len(raw)){return nil,offset,errors.New("truncated Huoshan length-prefixed content")};end:=next+int(size);return append([]byte(nil),raw[next:end]...),end,nil}
func huoshanRandomID()(string,error){var raw [16]byte;if _,err:=rand.Read(raw[:]);err!=nil{return "",err};return hex.EncodeToString(raw[:]),nil}
var _ StreamingTTSProvider = (*HuoshanDoubleStreamTTSProvider)(nil)
