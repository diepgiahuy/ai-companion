package pgstore

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"companion-server/internal/controlplane"
	"companion-server/internal/domain"
	"github.com/jackc/pgx/v5"
)

func (s *Store) ensureTwin(ctx context.Context, userID, deviceID string) error {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" { return fmt.Errorf("device_id required") }
	_, err := s.pool.Exec(ctx, `INSERT INTO device_twins(device_id,user_id,updated_at) VALUES($1,$2,$3) ON CONFLICT(device_id) DO NOTHING`, deviceID, owner(userID), time.Now().UTC())
	return err
}

func (s *Store) GetTwin(ctx context.Context, userID, deviceID string) (controlplane.Twin, error) {
	if err := s.ensureTwin(ctx,userID,deviceID); err != nil { return controlplane.Twin{},err }
	var t controlplane.Twin; var desired,reported []byte
	err:=s.pool.QueryRow(ctx,`SELECT device_id,user_id,desired_json,desired_version,reported_json,reported_version,updated_at FROM device_twins WHERE device_id=$1`,strings.TrimSpace(deviceID)).Scan(&t.DeviceID,&t.UserID,&desired,&t.DesiredVersion,&reported,&t.ReportedVersion,&t.UpdatedAt)
	if err!=nil{return t,err}
	if err:=json.Unmarshal(desired,&t.Desired);err!=nil{return t,fmt.Errorf("decode desired config: %w",err)}
	if err:=json.Unmarshal(reported,&t.Reported);err!=nil{return t,fmt.Errorf("decode reported config: %w",err)}
	t.UpdatedAt=t.UpdatedAt.UTC();return t,nil
}

func (s *Store) SaveTwin(ctx context.Context, t controlplane.Twin) error {
	if strings.TrimSpace(t.DeviceID)=="" { return fmt.Errorf("device_id required") }
	desired,err:=json.Marshal(t.Desired);if err!=nil{return err};reported,err:=json.Marshal(t.Reported);if err!=nil{return err}
	updated:=t.UpdatedAt;if updated.IsZero(){updated=time.Now().UTC()}
	_,err=s.pool.Exec(ctx,`INSERT INTO device_twins(device_id,user_id,desired_json,desired_version,reported_json,reported_version,updated_at) VALUES($1,$2,$3::jsonb,$4,$5::jsonb,$6,$7)
		ON CONFLICT(device_id) DO UPDATE SET user_id=EXCLUDED.user_id,desired_json=EXCLUDED.desired_json,desired_version=EXCLUDED.desired_version,reported_json=EXCLUDED.reported_json,reported_version=EXCLUDED.reported_version,updated_at=EXCLUDED.updated_at`,t.DeviceID,owner(t.UserID),string(desired),t.DesiredVersion,string(reported),t.ReportedVersion,updated.UTC())
	return err
}

func (s *Store) SetScopedConfig(ctx context.Context, scopeType, scopeID string, config controlplane.RuntimeConfig) error {
	scopeType=strings.TrimSpace(scopeType);scopeID=strings.TrimSpace(scopeID)
	if scopeType==""||scopeID==""{return fmt.Errorf("scope type and id required")}
	switch scopeType{case "global","tenant","plan","user","device":default:return fmt.Errorf("unsupported config scope %q",scopeType)}
	raw,err:=json.Marshal(config);if err!=nil{return err}
	_,err=s.pool.Exec(ctx,`INSERT INTO config_overrides(scope_type,scope_id,config_json,version,updated_at) VALUES($1,$2,$3::jsonb,1,$4)
		ON CONFLICT(scope_type,scope_id) DO UPDATE SET config_json=EXCLUDED.config_json,version=config_overrides.version+1,updated_at=EXCLUDED.updated_at`,scopeType,scopeID,string(raw),time.Now().UTC())
	return err
}

func (s *Store) GetScopedConfig(ctx context.Context, scopeType, scopeID string) (controlplane.ConfigOverride,bool,error) {
	var out controlplane.ConfigOverride;var raw []byte
	err:=s.pool.QueryRow(ctx,`SELECT scope_type,scope_id,config_json,version,updated_at FROM config_overrides WHERE scope_type=$1 AND scope_id=$2`,strings.TrimSpace(scopeType),strings.TrimSpace(scopeID)).Scan(&out.ScopeType,&out.ScopeID,&raw,&out.Version,&out.UpdatedAt)
	if err==pgx.ErrNoRows{return out,false,nil};if err!=nil{return out,false,err}
	if err:=json.Unmarshal(raw,&out.Config);err!=nil{return out,false,err};out.UpdatedAt=out.UpdatedAt.UTC();return out,true,nil
}

func (s *Store) BumpGeneration(ctx context.Context) (int64,error) {
	var version int64
	err:=s.pool.QueryRow(ctx,`UPDATE config_generation SET version=version+1 WHERE id=1 RETURNING version`).Scan(&version)
	return version,err
}
func (s *Store) ConfigGeneration(ctx context.Context)(int64,error){var version int64;err:=s.pool.QueryRow(ctx,`SELECT version FROM config_generation WHERE id=1`).Scan(&version);return version,err}

func (s *Store) Flags(ctx context.Context)([]controlplane.Flag,error){
	rows,err:=s.pool.Query(ctx,`SELECT key,enabled,rollout,required_plan,variants_json,lifecycle,owner,COALESCE(expires_at,'epoch'::timestamptz),expires_at IS NOT NULL FROM feature_flags ORDER BY key`);if err!=nil{return nil,err};defer rows.Close();var out []controlplane.Flag
	for rows.Next(){var f controlplane.Flag;var raw []byte;var exp time.Time;var hasExp bool;if err:=rows.Scan(&f.Key,&f.Enabled,&f.Rollout,&f.RequiredPlan,&raw,&f.Lifecycle,&f.Owner,&exp,&hasExp);err!=nil{return nil,err};if len(raw)>0{_ = json.Unmarshal(raw,&f.Variants)};if hasExp{exp=exp.UTC();f.ExpiresAt=&exp};out=append(out,f)};return out,rows.Err()
}
func (s *Store) SetFlag(ctx context.Context,f controlplane.Flag)error{
	if strings.TrimSpace(f.Key)==""||f.Rollout<0||f.Rollout>100{return fmt.Errorf("invalid flag")};if f.Lifecycle==""{f.Lifecycle="released"};raw,err:=json.Marshal(f.Variants);if err!=nil{return err}
	_,err=s.pool.Exec(ctx,`INSERT INTO feature_flags(key,enabled,rollout,required_plan,variants_json,lifecycle,owner,expires_at,updated_at) VALUES($1,$2,$3,$4,$5::jsonb,$6,$7,$8,$9)
		ON CONFLICT(key) DO UPDATE SET enabled=EXCLUDED.enabled,rollout=EXCLUDED.rollout,required_plan=EXCLUDED.required_plan,variants_json=EXCLUDED.variants_json,lifecycle=EXCLUDED.lifecycle,owner=EXCLUDED.owner,expires_at=EXCLUDED.expires_at,updated_at=EXCLUDED.updated_at`,f.Key,f.Enabled,f.Rollout,f.RequiredPlan,string(raw),f.Lifecycle,f.Owner,f.ExpiresAt,time.Now().UTC());return err
}
func (s *Store) EnsureFlag(ctx context.Context,f controlplane.Flag)error{var n int;err:=s.pool.QueryRow(ctx,`SELECT count(*) FROM feature_flags WHERE key=$1`,f.Key).Scan(&n);if err!=nil{return err};if n>0{return nil};return s.SetFlag(ctx,f)}

func (s *Store) SetEntitlement(ctx context.Context,userID,key string,enabled bool,expiresAt *time.Time)error{
	userID=owner(userID);key=strings.TrimSpace(key);if key==""{return fmt.Errorf("entitlement key required")}
	_,err:=s.pool.Exec(ctx,`INSERT INTO entitlements(subject_type,subject_id,entitlement,enabled,expires_at,updated_at) VALUES('user',$1,$2,$3,$4,$5)
		ON CONFLICT(subject_type,subject_id,entitlement) DO UPDATE SET enabled=EXCLUDED.enabled,expires_at=EXCLUDED.expires_at,updated_at=EXCLUDED.updated_at`,userID,key,enabled,expiresAt,time.Now().UTC());return err
}
func (s *Store) Allowed(ctx context.Context,userID,key string)bool{
	var enabled bool;var exp time.Time;var hasExp bool
	err:=s.pool.QueryRow(ctx,`SELECT enabled,COALESCE(expires_at,'epoch'::timestamptz),expires_at IS NOT NULL FROM entitlements WHERE subject_type='user' AND subject_id=$1 AND entitlement=$2`,owner(userID),strings.TrimSpace(key)).Scan(&enabled,&exp,&hasExp)
	if err!=nil||!enabled{return false};return !hasExp||exp.After(time.Now())
}

func (s *Store) EnrollDevice(ctx context.Context,identity domain.Identity,rawToken string)error{
	identity.UserID=owner(identity.UserID);identity.DeviceID=strings.TrimSpace(identity.DeviceID);if identity.DeviceID==""||len(rawToken)<16{return fmt.Errorf("user/device and token>=16 required")}
	h:=sha256.Sum256([]byte(rawToken));now:=time.Now().UTC()
	_,err:=s.pool.Exec(ctx,`INSERT INTO device_credentials(device_id,user_id,tenant_id,plan,token_sha256,status,created_at,rotated_at) VALUES($1,$2,$3,$4,$5,'active',$6,$6)
		ON CONFLICT(device_id) DO UPDATE SET user_id=EXCLUDED.user_id,tenant_id=EXCLUDED.tenant_id,plan=EXCLUDED.plan,token_sha256=EXCLUDED.token_sha256,status='active',rotated_at=EXCLUDED.rotated_at`,identity.DeviceID,identity.UserID,strings.TrimSpace(identity.TenantID),strings.TrimSpace(identity.Plan),hex.EncodeToString(h[:]),now);return err
}
func (s *Store) AuthenticateDevice(ctx context.Context,deviceID,rawToken string)(domain.Identity,bool,error){
	deviceID=strings.TrimSpace(deviceID);h:=sha256.Sum256([]byte(rawToken));var identity domain.Identity;identity.DeviceID=deviceID;var status,stored string
	err:=s.pool.QueryRow(ctx,`SELECT user_id,tenant_id,plan,status,token_sha256 FROM device_credentials WHERE device_id=$1`,deviceID).Scan(&identity.UserID,&identity.TenantID,&identity.Plan,&status,&stored);if err==pgx.ErrNoRows{return identity,false,nil};if err!=nil{return identity,false,err};decoded,err:=hex.DecodeString(stored);if err!=nil{return identity,false,nil};return identity,status=="active"&&len(decoded)==sha256.Size&&subtle.ConstantTimeCompare(decoded,h[:])==1,nil
}
func (s *Store) RevokeDevice(ctx context.Context,deviceID string)error{tag,err:=s.pool.Exec(ctx,`UPDATE device_credentials SET status='revoked',rotated_at=$1 WHERE device_id=$2`,time.Now().UTC(),strings.TrimSpace(deviceID));if err!=nil{return err};return requireRowsChanged(tag.RowsAffected(),"device credential")}

func (s *Store) PublishFirmware(ctx context.Context,m controlplane.FirmwareManifest)error{
	raw,err:=json.Marshal(m);if err!=nil{return err}
	_,err=s.pool.Exec(ctx,`INSERT INTO firmware_releases(metadata_version,version,channel,board,protocol_min,security_version,url,sha256,size,expires_at,signature,manifest_json,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::jsonb,$13)
		ON CONFLICT(metadata_version) DO UPDATE SET version=EXCLUDED.version,channel=EXCLUDED.channel,board=EXCLUDED.board,protocol_min=EXCLUDED.protocol_min,security_version=EXCLUDED.security_version,url=EXCLUDED.url,sha256=EXCLUDED.sha256,size=EXCLUDED.size,expires_at=EXCLUDED.expires_at,signature=EXCLUDED.signature,manifest_json=EXCLUDED.manifest_json`,m.MetadataVersion,m.Version,m.Channel,m.Board,m.ProtocolMin,m.SecurityVersion,m.URL,m.SHA256,m.Size,m.ExpiresAt.UTC(),m.Signature,string(raw),time.Now().UTC());return err
}
func (s *Store) LatestFirmware(ctx context.Context,channel,board string)([]controlplane.FirmwareManifest,error){
	rows,err:=s.pool.Query(ctx,`SELECT manifest_json FROM firmware_releases WHERE channel=$1 AND board=$2 ORDER BY metadata_version DESC`,channel,board);if err!=nil{return nil,err};defer rows.Close();var out []controlplane.FirmwareManifest
	for rows.Next(){var raw []byte;if err:=rows.Scan(&raw);err!=nil{return nil,err};var m controlplane.FirmwareManifest;if err:=json.Unmarshal(raw,&m);err!=nil{return nil,err};out=append(out,m)};return out,rows.Err()
}

func (s *Store) PutFeatureModule(ctx context.Context,m controlplane.FeatureModule)error{raw,err:=json.Marshal(m);if err!=nil{return err};_,err=s.pool.Exec(ctx,`INSERT INTO feature_modules(id,version,lifecycle,execution,manifest_json,updated_at) VALUES($1,$2,$3,$4,$5::jsonb,$6) ON CONFLICT(id) DO UPDATE SET version=EXCLUDED.version,lifecycle=EXCLUDED.lifecycle,execution=EXCLUDED.execution,manifest_json=EXCLUDED.manifest_json,updated_at=EXCLUDED.updated_at`,m.ID,m.Version,m.Lifecycle,m.Execution,string(raw),time.Now().UTC());return err}
func (s *Store) FeatureModules(ctx context.Context)([]controlplane.FeatureModule,error){rows,err:=s.pool.Query(ctx,`SELECT manifest_json FROM feature_modules ORDER BY id`);if err!=nil{return nil,err};defer rows.Close();var out []controlplane.FeatureModule;for rows.Next(){var raw []byte;if err:=rows.Scan(&raw);err!=nil{return nil,err};var m controlplane.FeatureModule;if err:=json.Unmarshal(raw,&m);err!=nil{return nil,err};out=append(out,m)};return out,rows.Err()}

var _ controlplane.Repository = (*Store)(nil)
var _ controlplane.FlagAdminRepository = (*Store)(nil)
var _ controlplane.FirmwareRepository = (*Store)(nil)
var _ controlplane.ModuleRepository = (*Store)(nil)
