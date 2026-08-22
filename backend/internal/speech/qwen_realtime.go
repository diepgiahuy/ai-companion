package speech

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"unicode"

	"github.com/coder/websocket"
)

type QwenRealtimeConfig struct {
	URL string
	Model string
	APIKey string
	WorkspaceID string
	Voice string
	Instructions string
	TurnDetection string
	VADThreshold float64
	SilenceMS int
	Tools []NativeRealtimeTool
	HTTPClient *http.Client
}

func (c QwenRealtimeConfig) normalized()(QwenRealtimeConfig,error){
	c.URL=strings.TrimSpace(c.URL); c.Model=strings.TrimSpace(c.Model); c.APIKey=strings.TrimSpace(c.APIKey)
	if c.URL==""||c.Model==""||c.APIKey==""{return c,errors.New("Qwen Realtime URL, model and API key are required")}
	parsed,err:=url.Parse(c.URL); if err!=nil||parsed.Scheme==""||parsed.Host==""{return c,fmt.Errorf("invalid Qwen Realtime URL %q",c.URL)}
	if parsed.Scheme!="wss"&&parsed.Hostname()!="localhost"&&parsed.Hostname()!="127.0.0.1"{return c,errors.New("Qwen Realtime URL must use wss outside localhost")}
	if c.Voice==""{c.Voice="longanqian"}; if c.TurnDetection==""{c.TurnDetection="manual"}; switch c.TurnDetection{case "manual","server_vad","smart_turn":default:return c,fmt.Errorf("unsupported Qwen Realtime turn detection %q",c.TurnDetection)}; if c.VADThreshold==0{c.VADThreshold=0.5}; if c.SilenceMS<=0{c.SilenceMS=800}; return c,nil
}

type QwenRealtimeProvider struct{config QwenRealtimeConfig}
func NewQwenRealtime(config QwenRealtimeConfig)(*QwenRealtimeProvider,error){normalized,err:=config.normalized();if err!=nil{return nil,err};return &QwenRealtimeProvider{config:normalized},nil}
func (p *QwenRealtimeProvider) Connect(ctx context.Context)(NativeRealtimeSession,error){
	if p==nil{return nil,errors.New("Qwen Realtime provider is nil")};parsed,err:=url.Parse(p.config.URL);if err!=nil{return nil,err};query:=parsed.Query();query.Set("model",p.config.Model);parsed.RawQuery=query.Encode();headers:=http.Header{};headers.Set("Authorization","Bearer "+p.config.APIKey);headers.Set("User-Agent","ai-companion/qwen-realtime-reference");if strings.TrimSpace(p.config.WorkspaceID)!=""{headers.Set("X-DashScope-WorkSpace",strings.TrimSpace(p.config.WorkspaceID))};options:=&websocket.DialOptions{HTTPHeader:headers};if p.config.HTTPClient!=nil{options.HTTPClient=p.config.HTTPClient};conn,_,err:=websocket.Dial(ctx,parsed.String(),options);if err!=nil{return nil,fmt.Errorf("dial Qwen Realtime: %w",err)}
	aliases,tools,err:=qwenRealtimeTools(p.config.Tools);if err!=nil{conn.CloseNow();return nil,err};s:=&qwenRealtimeSession{conn:conn,aliasToCanonical:aliases};session:=map[string]any{"modalities":[]string{"audio","text"},"voice":p.config.Voice,"input_audio_format":"pcm","output_audio_format":"pcm"};if p.config.Instructions!=""{session["instructions"]=p.config.Instructions};if len(tools)>0{session["tools"]=tools};if p.config.TurnDetection=="manual"{session["turn_detection"]=nil}else{turn:=map[string]any{"type":p.config.TurnDetection};if p.config.TurnDetection=="server_vad"{turn["threshold"]=p.config.VADThreshold;turn["silence_duration_ms"]=p.config.SilenceMS};session["turn_detection"]=turn};if err:=s.writeJSON(ctx,map[string]any{"type":"session.update","session":session});err!=nil{conn.CloseNow();return nil,fmt.Errorf("configure Qwen Realtime session: %w",err)};return s,nil
}

type qwenRealtimeSession struct{conn *websocket.Conn;writeMu sync.Mutex;closeOnce sync.Once;aliasToCanonical map[string]string}
func(s *qwenRealtimeSession)AppendAudio(ctx context.Context,pcm []byte)error{if len(pcm)==0{return nil};if len(pcm)%2!=0{return errors.New("Qwen Realtime PCM16 input must contain an even number of bytes")};return s.writeJSON(ctx,map[string]any{"type":"input_audio_buffer.append","audio":base64.StdEncoding.EncodeToString(pcm)})}
func(s *qwenRealtimeSession)CommitAudio(ctx context.Context)error{return s.writeJSON(ctx,map[string]any{"type":"input_audio_buffer.commit"})}
func(s *qwenRealtimeSession)CreateResponse(ctx context.Context)error{return s.writeJSON(ctx,map[string]any{"type":"response.create","response":map[string]any{"modalities":[]string{"audio","text"}}})}
func(s *qwenRealtimeSession)CancelResponse(ctx context.Context)error{return s.writeJSON(ctx,map[string]any{"type":"response.cancel"})}
func(s *qwenRealtimeSession)ReturnToolResult(ctx context.Context,callID,output string)error{callID=strings.TrimSpace(callID);if callID==""{return errors.New("Qwen Realtime tool call id is required")};if err:=s.writeJSON(ctx,map[string]any{"type":"conversation.item.create","item":map[string]any{"type":"function_call_output","call_id":callID,"output":output}});err!=nil{return err};return s.CreateResponse(ctx)}
func(s *qwenRealtimeSession)writeJSON(ctx context.Context,value any)error{raw,err:=json.Marshal(value);if err!=nil{return err};s.writeMu.Lock();defer s.writeMu.Unlock();return s.conn.Write(ctx,websocket.MessageText,raw)}
func(s *qwenRealtimeSession)Receive(ctx context.Context)(NativeRealtimeEvent,error){
	kind,raw,err:=s.conn.Read(ctx);if err!=nil{if ctx.Err()!=nil{return NativeRealtimeEvent{},ctx.Err()};return NativeRealtimeEvent{},fmt.Errorf("read Qwen Realtime event: %w",err)};if kind!=websocket.MessageText{return NativeRealtimeEvent{},errors.New("Qwen Realtime returned non-JSON event")};var event qwenRealtimeWireEvent;if err:=json.Unmarshal(raw,&event);err!=nil{return NativeRealtimeEvent{},fmt.Errorf("decode Qwen Realtime event: %w",err)};out:=NativeRealtimeEvent{Type:event.Type};switch event.Type{case "error":return out,fmt.Errorf("Qwen Realtime error %s/%s: %s",event.Error.Type,event.Error.Code,event.Error.Message);case "conversation.item.input_audio_transcription.delta":out.InputTranscript=event.Text+event.Stash;case "conversation.item.input_audio_transcription.completed":out.InputTranscript,out.InputFinal=event.Transcript,true;case "response.text.delta":out.TextDelta=event.Delta;case "response.audio_transcript.delta":out.AudioTranscript=event.Delta;case "response.audio_transcript.done":out.AudioTranscript=event.Transcript;case "response.audio.delta":pcm,err:=base64.StdEncoding.DecodeString(event.Delta);if err!=nil{return out,fmt.Errorf("decode Qwen Realtime PCM: %w",err)};out.AudioPCM=pcm;case "response.function_call_arguments.done":canonical,ok:=s.aliasToCanonical[event.Name];if !ok{return out,fmt.Errorf("Qwen Realtime returned undeclared tool alias %q",event.Name)};args:=map[string]any{};if strings.TrimSpace(event.Arguments)!=""{if err:=json.Unmarshal([]byte(event.Arguments),&args);err!=nil{return out,fmt.Errorf("decode Qwen Realtime tool args: %w",err)}};out.ToolCall=&NativeRealtimeToolCall{CallID:event.CallID,Name:canonical,Arguments:args};case "response.done":out.ResponseDone=true;out.ResponseStatus=event.Response.Status};return out,nil
}
func(s *qwenRealtimeSession)Close()error{var err error;s.closeOnce.Do(func(){err=s.conn.Close(websocket.StatusNormalClosure,"Qwen Realtime reference closed")});return err}
type qwenRealtimeWireEvent struct{Type string `json:"type"`;Text string `json:"text"`;Stash string `json:"stash"`;Transcript string `json:"transcript"`;Delta string `json:"delta"`;CallID string `json:"call_id"`;Name string `json:"name"`;Arguments string `json:"arguments"`;Error struct{Type string `json:"type"`;Code string `json:"code"`;Message string `json:"message"`} `json:"error"`;Response struct{Status string `json:"status"`} `json:"response"`}
func qwenRealtimeTools(tools []NativeRealtimeTool)(map[string]string,[]map[string]any,error){aliases:=make(map[string]string,len(tools));result:=make([]map[string]any,0,len(tools));for _,tool:=range tools{canonical:=strings.TrimSpace(tool.Name);if canonical==""{return nil,nil,errors.New("Qwen Realtime tool name is required")};alias:=qwenRealtimeToolAlias(canonical);if previous,exists:=aliases[alias];exists&&previous!=canonical{return nil,nil,fmt.Errorf("Qwen Realtime tool alias collision for %q and %q",previous,canonical)};aliases[alias]=canonical;function:=map[string]any{"name":alias,"description":tool.Description};if tool.Parameters!=nil{function["parameters"]=tool.Parameters};result=append(result,map[string]any{"type":"function","function":function})};return aliases,result,nil}
func qwenRealtimeToolAlias(canonical string)string{var slug strings.Builder;for _,r:=range canonical{if unicode.IsLetter(r)||unicode.IsDigit(r)||r=='_'||r=='-'{slug.WriteRune(r)}else{slug.WriteByte('_')};if slug.Len()>=32{break}};if slug.Len()==0{slug.WriteString("tool")};sum:=sha256.Sum256([]byte(canonical));return "c_"+slug.String()+"_"+hex.EncodeToString(sum[:8])}
var _ NativeRealtimeSession = (*qwenRealtimeSession)(nil)
