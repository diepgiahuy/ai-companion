package pgstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"companion-server/internal/memory"
	"companion-server/internal/privacy"
	"companion-server/internal/usage"
	"github.com/jackc/pgx/v5"
)

func (s *Store) UpsertMemory(ctx context.Context, item memory.Item) (memory.Item, error) {
	if item.UserID == "" || item.Key == "" || item.Value == "" {
		return item, fmt.Errorf("user, key and value required")
	}
	if item.ValidFrom.IsZero() { item.ValidFrom = time.Now().UTC() }
	if item.CreatedAt.IsZero() { item.CreatedAt = time.Now().UTC() }
	embedding, err := json.Marshal(item.Embedding); if err != nil { return item, err }
	tx, err := s.pool.Begin(ctx); if err != nil { return item, err }; defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE memories SET valid_to=$1 WHERE user_id=$2 AND memory_key=$3 AND valid_to IS NULL AND deleted_at IS NULL`, item.ValidFrom.UTC(), item.UserID, item.Key); err != nil { return item, err }
	if err = tx.QueryRow(ctx, `INSERT INTO memories(user_id,memory_key,kind,value,valid_from,source,confidence,embedding,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9) RETURNING id`, item.UserID,item.Key,string(item.Kind),item.Value,item.ValidFrom.UTC(),item.Source,item.Confidence,string(embedding),item.CreatedAt.UTC()).Scan(&item.ID); err != nil { return item, err }
	if err = tx.Commit(ctx); err != nil { return item, err }; item.ValidFrom=item.ValidFrom.UTC(); item.CreatedAt=item.CreatedAt.UTC(); return item,nil
}

func (s *Store) CurrentMemories(ctx context.Context,userID string,now time.Time,limit int)([]memory.Item,error){
	if limit<=0||limit>500{limit=200};rows,err:=s.pool.Query(ctx,`SELECT id,user_id,memory_key,kind,value,valid_from,COALESCE(valid_to,'epoch'::timestamptz),valid_to IS NOT NULL,source,confidence,embedding,created_at FROM memories WHERE user_id=$1 AND deleted_at IS NULL AND valid_from<=$2 AND (valid_to IS NULL OR valid_to>$2) ORDER BY valid_from DESC LIMIT $3`,owner(userID),now.UTC(),limit);if err!=nil{return nil,err};defer rows.Close();var out []memory.Item
	for rows.Next(){var item memory.Item;var kind string;var validTo time.Time;var hasValidTo bool;var raw []byte;if err:=rows.Scan(&item.ID,&item.UserID,&item.Key,&kind,&item.Value,&item.ValidFrom,&validTo,&hasValidTo,&item.Source,&item.Confidence,&raw,&item.CreatedAt);err!=nil{return nil,err};item.Kind=memory.Kind(kind);item.ValidFrom=item.ValidFrom.UTC();item.CreatedAt=item.CreatedAt.UTC();if hasValidTo{validTo=validTo.UTC();item.ValidTo=&validTo};if len(raw)>0{_ = json.Unmarshal(raw,&item.Embedding)};out=append(out,item)};return out,rows.Err()
}

func (s *Store) ForgetMemory(ctx context.Context,userID,key string)error{now:=time.Now().UTC();tag,err:=s.pool.Exec(ctx,`UPDATE memories SET deleted_at=$1,valid_to=COALESCE(valid_to,$1) WHERE user_id=$2 AND memory_key=$3 AND deleted_at IS NULL`,now,owner(userID),key);if err!=nil{return err};return requireRowsChanged(tag.RowsAffected(),"memory key")}

func (s *Store) GetPrivacyPolicy(ctx context.Context,userID string)(privacy.Policy,bool,error){var p privacy.Policy;err:=s.pool.QueryRow(ctx,`SELECT user_id,save_voice_audio,long_term_memory_enabled,conversation_retention_days,voice_memo_retention_days,memory_retention_days,updated_at FROM privacy_policies WHERE user_id=$1`,owner(userID)).Scan(&p.UserID,&p.SaveVoiceAudio,&p.LongTermMemoryEnabled,&p.ConversationRetentionDays,&p.VoiceMemoRetentionDays,&p.MemoryRetentionDays,&p.UpdatedAt);if err==pgx.ErrNoRows{return p,false,nil};if err!=nil{return p,false,err};p.UpdatedAt=p.UpdatedAt.UTC();return p,true,nil}
func (s *Store) SetPrivacyPolicy(ctx context.Context,p privacy.Policy)error{if p.UpdatedAt.IsZero(){p.UpdatedAt=time.Now().UTC()};_,err:=s.pool.Exec(ctx,`INSERT INTO privacy_policies(user_id,save_voice_audio,long_term_memory_enabled,conversation_retention_days,voice_memo_retention_days,memory_retention_days,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(user_id) DO UPDATE SET save_voice_audio=EXCLUDED.save_voice_audio,long_term_memory_enabled=EXCLUDED.long_term_memory_enabled,conversation_retention_days=EXCLUDED.conversation_retention_days,voice_memo_retention_days=EXCLUDED.voice_memo_retention_days,memory_retention_days=EXCLUDED.memory_retention_days,updated_at=EXCLUDED.updated_at`,owner(p.UserID),p.SaveVoiceAudio,p.LongTermMemoryEnabled,p.ConversationRetentionDays,p.VoiceMemoRetentionDays,p.MemoryRetentionDays,p.UpdatedAt.UTC());return err}

func (s *Store) ApplyRetention(ctx context.Context,now time.Time)(privacy.RetentionReport,error){
	rows,err:=s.pool.Query(ctx,`SELECT user_id,conversation_retention_days,voice_memo_retention_days,memory_retention_days FROM privacy_policies WHERE conversation_retention_days>0 OR voice_memo_retention_days>0 OR memory_retention_days>0`);if err!=nil{return privacy.RetentionReport{},err};defer rows.Close();type policyRow struct{user string;conversation,voice,memory int};var policies []policyRow;for rows.Next(){var p policyRow;if err:=rows.Scan(&p.user,&p.conversation,&p.voice,&p.memory);err!=nil{return privacy.RetentionReport{},err};policies=append(policies,p)};if err:=rows.Err();err!=nil{return privacy.RetentionReport{},err}
	var report privacy.RetentionReport
	for _,p:=range policies{
		if p.conversation>0{tag,err:=s.pool.Exec(ctx,`DELETE FROM conversation_messages WHERE user_id=$1 AND created_at<$2`,p.user,now.Add(-time.Duration(p.conversation)*24*time.Hour).UTC());if err!=nil{return report,err};report.ConversationRows+=tag.RowsAffected()}
		if p.memory>0{tag,err:=s.pool.Exec(ctx,`DELETE FROM memories WHERE user_id=$1 AND created_at<$2`,p.user,now.Add(-time.Duration(p.memory)*24*time.Hour).UTC());if err!=nil{return report,err};report.MemoryRows+=tag.RowsAffected()}
		if p.voice>0{cutoff:=now.Add(-time.Duration(p.voice)*24*time.Hour).UTC();voiceRows,err:=s.pool.Query(ctx,`SELECT path FROM voice_memos WHERE user_id=$1 AND created_at<$2`,p.user,cutoff);if err!=nil{return report,err};var paths []string;for voiceRows.Next(){var path string;if err:=voiceRows.Scan(&path);err!=nil{voiceRows.Close();return report,err};paths=append(paths,path)};if err:=voiceRows.Err();err!=nil{voiceRows.Close();return report,err};voiceRows.Close();tag,err:=s.pool.Exec(ctx,`DELETE FROM voice_memos WHERE user_id=$1 AND created_at<$2`,p.user,cutoff);if err!=nil{return report,err};report.VoiceMemoRows+=tag.RowsAffected();report.OrphanPaths=append(report.OrphanPaths,paths...)}
	}
	return report,nil
}
func (s *Store) ReferencedVoiceMemoPaths(ctx context.Context)([]string,error){rows,err:=s.pool.Query(ctx,`SELECT path FROM voice_memos WHERE path<>'' ORDER BY path`);if err!=nil{return nil,err};defer rows.Close();var out []string;for rows.Next(){var path string;if err:=rows.Scan(&path);err!=nil{return nil,err};out=append(out,path)};return out,rows.Err()}

func (s *Store) RecordUsage(ctx context.Context,r usage.Record)error{if r.PromptTokens<0||r.CompletionTokens<0||r.TotalTokens<0{return fmt.Errorf("usage token counts must be non-negative")};if r.CreatedAt.IsZero(){r.CreatedAt=time.Now().UTC()};_,err:=s.pool.Exec(ctx,`INSERT INTO llm_usage(user_id,device_id,provider,model,prompt_version,prompt_tokens,completion_tokens,total_tokens,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`,owner(r.UserID),r.DeviceID,r.Provider,r.Model,r.PromptVersion,r.PromptTokens,r.CompletionTokens,r.TotalTokens,r.CreatedAt.UTC());return err}
func (s *Store) MonthlyUsage(ctx context.Context,userID string,month time.Time)(int64,error){month=month.UTC();start:=time.Date(month.Year(),month.Month(),1,0,0,0,0,time.UTC);end:=start.AddDate(0,1,0);var total int64;err:=s.pool.QueryRow(ctx,`SELECT COALESCE(SUM(total_tokens),0) FROM llm_usage WHERE user_id=$1 AND created_at>=$2 AND created_at<$3`,owner(userID),start,end).Scan(&total);return total,err}

var _ memory.Repository = (*Store)(nil)
var _ privacy.Repository = (*Store)(nil)
var _ usage.Meter = (*Store)(nil)
var _ usage.Reader = (*Store)(nil)
