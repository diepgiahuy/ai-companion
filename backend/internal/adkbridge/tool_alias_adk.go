//go:build adk

package adkbridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"iter"
	"strings"
	"unicode"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

type providerToolAliasModel struct { inner model.LLM }

func withProviderToolAliases(inner model.LLM) model.LLM {
	if inner == nil { return nil }
	return &providerToolAliasModel{inner: inner}
}
func (m *providerToolAliasModel) Name() string { return m.inner.Name() }
func (m *providerToolAliasModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		aliased, reverse, err := aliasModelRequest(req)
		if err != nil { yield(nil, err); return }
		for response, err := range m.inner.GenerateContent(ctx, aliased, stream) {
			if err != nil { yield(nil, err); return }
			if response != nil && response.Content != nil {
				for _, part := range response.Content.Parts {
					if part == nil || part.FunctionCall == nil { continue }
					canonical, ok := reverse[part.FunctionCall.Name]
					if !ok { yield(nil, fmt.Errorf("provider returned undeclared tool alias %q", part.FunctionCall.Name)); return }
					part.FunctionCall.Name = canonical
				}
			}
			if !yield(response, nil) { return }
		}
	}
}

func aliasModelRequest(req *model.LLMRequest) (*model.LLMRequest, map[string]string, error) {
	if req == nil { return nil, nil, fmt.Errorf("provider tool alias: request is nil") }
	cloned := *req
	var err error
	cloned.Contents, err = cloneContents(req.Contents); if err != nil { return nil,nil,err }
	cloned.Config, err = cloneGenerateConfig(req.Config); if err != nil { return nil,nil,err }
	forward := map[string]string{}; reverse := map[string]string{}
	if cloned.Config != nil {
		for _, tool := range cloned.Config.Tools {
			if tool == nil { continue }
			for _, declaration := range tool.FunctionDeclarations {
				if declaration == nil || strings.TrimSpace(declaration.Name)=="" { continue }
				canonical:=declaration.Name; alias:=providerToolAlias(canonical)
				if existing,ok:=reverse[alias];ok&&existing!=canonical{return nil,nil,fmt.Errorf("provider tool alias collision %q for %q and %q",alias,existing,canonical)}
				forward[canonical]=alias; reverse[alias]=canonical; declaration.Name=alias
			}
		}
		if cfg:=cloned.Config.ToolConfig;cfg!=nil&&cfg.FunctionCallingConfig!=nil{
			for i,canonical:=range cfg.FunctionCallingConfig.AllowedFunctionNames{alias,ok:=forward[canonical];if !ok{return nil,nil,fmt.Errorf("provider tool alias: allowed function %q is not declared",canonical)};cfg.FunctionCallingConfig.AllowedFunctionNames[i]=alias}
		}
	}
	for _,content:=range cloned.Contents{if content==nil{continue};for _,part:=range content.Parts{if part==nil{continue};if part.FunctionCall!=nil{alias,ok:=forward[part.FunctionCall.Name];if !ok{return nil,nil,fmt.Errorf("provider tool alias: historical function call %q is not declared",part.FunctionCall.Name)};part.FunctionCall.Name=alias};if part.FunctionResponse!=nil{alias,ok:=forward[part.FunctionResponse.Name];if !ok{return nil,nil,fmt.Errorf("provider tool alias: historical function response %q is not declared",part.FunctionResponse.Name)};part.FunctionResponse.Name=alias}}}
	return &cloned, reverse, nil
}

func providerToolAlias(canonical string) string {
	var slug strings.Builder
	for _,r:=range canonical{if unicode.IsLetter(r)||unicode.IsDigit(r)||r=='_'||r=='-'{slug.WriteRune(r)}else{slug.WriteByte('_')};if slug.Len()>=32{break}}
	if slug.Len()==0{slug.WriteString("tool")}
	digest:=sha256.Sum256([]byte(canonical));return "c_"+slug.String()+"_"+hex.EncodeToString(digest[:8])
}
func cloneContents(contents []*genai.Content)([]*genai.Content,error){if contents==nil{return nil,nil};raw,err:=json.Marshal(contents);if err!=nil{return nil,fmt.Errorf("provider tool alias: clone contents: %w",err)};var cloned []*genai.Content;if err:=json.Unmarshal(raw,&cloned);err!=nil{return nil,fmt.Errorf("provider tool alias: clone contents: %w",err)};return cloned,nil}
func cloneGenerateConfig(config *genai.GenerateContentConfig)(*genai.GenerateContentConfig,error){if config==nil{return nil,nil};raw,err:=json.Marshal(config);if err!=nil{return nil,fmt.Errorf("provider tool alias: clone config: %w",err)};var cloned genai.GenerateContentConfig;if err:=json.Unmarshal(raw,&cloned);err!=nil{return nil,fmt.Errorf("provider tool alias: clone config: %w",err)};return &cloned,nil}
var _ model.LLM = (*providerToolAliasModel)(nil)
