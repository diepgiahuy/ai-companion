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
	if err := s.ensureTwin(ctx, userID, deviceID); err != nil { return controlplane.Twin{}, err }
	var twin controlplane.Twin
	var desired, reported []byte
	err := s.pool.QueryRow(ctx, `SELECT device_id,user_id,desired_json,desired_version,reported_json,reported_version,updated_at FROM device_twins WHERE device_id=$1`, strings.TrimSpace(deviceID)).Scan(&twin.DeviceID,&twin.UserID,&desired,&twin.DesiredVersion,&reported,&twin.ReportedVersion,&twin.UpdatedAt)
	if err != nil { return twin, err }
	if err := json.Unmarshal(desired,&twin.Desired); err != nil { return twin, fmt.Errorf("decode desired config: %w",err) }
	if err := json.Unmarshal(reported,&twin.Reported); err != nil { return twin, fmt.Errorf("decode reported config: %w",err) }
	twin.UpdatedAt=twin.UpdatedAt.UTC(); return twin,nil
}

func (s *Store) SetDesired(ctx context.Context, userID, deviceID string, config controlplane.RuntimeConfig) (controlplane.Twin, error) {
	if err := s.ensureTwin(ctx,userID,deviceID); err != nil { return controlplane.Twin{},err }
	raw,err:=json.Marshal(config); if err != nil { return controlplane.Twin{},err }
	var twin controlplane.Twin; var desired,reported []byte
	err=s.pool.QueryRow(ctx,`UPDATE device_twins SET user_id=$1,desired_json=$2::jsonb,desired_version=desired_version+1,updated_at=$3 WHERE device_id=$4 RETURNING device_id,user_id,desired_json,desired_version,reported_json,reported_version,updated_at`,owner(userID),string(raw),time.Now().UTC(),strings.TrimSpace(deviceID)).Scan(&twin.DeviceID,&twin.UserID,&desired,&twin.DesiredVersion,&reported,&twin.ReportedVersion,&twin.UpdatedAt)
	if err != nil { return controlplane.Twin{},err }
	if err:=json.Unmarshal(desired,&twin.Desired);err!=nil{return controlplane.Twin{},err};if err:=json.Unmarshal(reported,&twin.Reported);err!=nil{return controlplane.Twin{},err};twin.UpdatedAt=twin.UpdatedAt.UTC();return twin,nil
}

func (s *Store) Report(ctx context.Context,userID,deviceID string,version int64,config controlplane.RuntimeConfig) error {
	if version < 0 { return fmt.Errorf("reported version cannot be negative") }
	raw,err:=json.Marshal(config);if err!=nil{return err}
	tag,err:=s.pool.Exec(ctx,`UPDATE device_twins SET user_id=$1,reported_json=$2::jsonb,reported_version=$3,updated_at=$4 WHERE device_id=$5 AND desired_version >= $3`,owner(userID),string(raw),version,time.Now().UTC(),strings.TrimSpace(deviceID));if err!=nil{return err};return requireRowsChanged(tag.RowsAffected(),"device twin")
}

func (s *Store) GetConfigOverride(ctx context.Context,scopeType,scopeID string)(controlplane.RuntimeConfig,bool,error){
	var config controlplane.RuntimeConfig;var raw []byte;err:=s.pool.QueryRow(ctx,`SELECT config_json FROM config_overrides WHERE scope_type=$1 AND scope_id=$2`,strings.TrimSpace(scopeType),strings.TrimSpace(scopeID)).Scan(&raw);if err==pgx.ErrNoRows{return config,false,nil};if err!=nil{return config,false,err};if err:=json.Unmarshal(raw,&config);err!=nil{return config,false,fmt.Errorf("decode config override: %w",err)};return config,true,nil
}
func (s *Store) SetConfigOverride(ctx context.Context,scopeType,scopeID string,config controlplane.RuntimeConfig)error{
	scopeType=strings.TrimSpace(scopeType);scopeID=strings.TrimSpace(scopeID);if scopeType==""||scopeID==""{return fmt.Errorf("scope type and id required")};switch scopeType{case "global","tenant","plan","user":default:return fmt.Errorf("unsupported config scope %q",scopeType)};raw,err:=json.Marshal(config);if err!=nil{return err};_,err=s.pool.Exec(ctx,`INSERT INTO config_overrides(scope_type,scope_id,config_json,version,updated_at) VALUES($1,$2,$3::jsonb,1,$4) ON CONFLICT(scope_type,scope_id) DO UPDATE SET config_json=EXCLUDED.config_json,version=config_overrides.version+1,updated_at=EXCLUDED.updated_at`,scopeType,scopeID,string(raw),time.Now().UTC());return err
}

func (s *Store) Flags(ctx context.Context)([]controlplane.Flag,error){
	rows,err:=s.pool.Query(ctx,`SELECT key,enabled,rollout,required_plan,variants_json,lifecycle,owner,COALESCE(expires_at,'epoch'::timestamptz),expires_at IS NOT NULL FROM feature_flags ORDER BY key`);if err!=nil{return nil,err};defer rows.Close();var out []controlplane.Flag
	for rows.Next(){var flag controlplane.Flag;var raw []byte;var expires time.Time;var hasExpires bool;if err:=rows.Scan(&flag.Key,&flag.Enabled,&flag.Rollout,&flag.RequiredPlan,&raw,&flag.Lifecycle,&flag.Owner,&expires,&hasExpires);err!=nil{return nil,err};if len(raw)>0{if err:=json.Unmarshal(raw,&flag.Variants);err!=nil{return nil,err}};if hasExpires{expires=expires.UTC();flag.ExpiresAt=&expires};out=append(out,flag)};return out,rows.Err()
}
func (s *Store) SetFlag(ctx context.Context,flag controlplane.Flag)error{
	if strings.TrimSpace(flag.Key)==""||flag.Rollout<0||flag.Rollout>100{return fmt.Errorf("invalid flag")};if flag.Lifecycle==""{flag.Lifecycle="released"};raw,err:=json.Marshal(flag.Variants);if err!=nil{return err};_,err=s.pool.Exec(ctx,`INSERT INTO feature_flags(key,enabled,rollout,required_plan,variants_json,lifecycle,owner,expires_at,updated_at) VALUES($1,$2,$3,$4,$5::jsonb,$6,$7,$8,$9) ON CONFLICT(key) DO UPDATE SET enabled=EXCLUDED.enabled,rollout=EXCLUDED.rollout,required_plan=EXCLUDED.required_plan,variants_json=EXCLUDED.variants_json,lifecycle=EXCLUDED.lifecycle,owner=EXCLUDED.owner,expires_at=EXCLUDED.expires_at,updated_at=EXCLUDED.updated_at`,flag.Key,flag.Enabled,flag.Rollout,flag.RequiredPlan,string(raw),flag.Lifecycle,flag.Owner,flag.ExpiresAt,time.Now().UTC());return err
}
func (s *Store) EnsureFlag(ctx context.Context,flag controlplane.Flag)error{var exists bool;if err:=s.pool.QueryRow(ctx,`SELECT EXISTS(SELECT 1 FROM feature_flags WHERE key=$1)`,flag.Key).Scan(&exists);err!=nil{return err};if exists{return nil};return s.SetFlag(ctx,flag)}

func (s *Store) SetEntitlement(ctx context.Context,userID,key string,enabled bool,expiresAt *time.Time)error{userID=owner(userID);key=strings.TrimSpace(key);if key==""{return fmt.Errorf("entitlement key required")};_,err:=s.pool.Exec(ctx,`INSERT INTO entitlements(subject_type,subject_id,entitlement,enabled,expires_at,updated_at) VALUES('user',$1,$2,$3,$4,$5) ON CONFLICT(subject_type,subject_id,entitlement) DO UPDATE SET enabled=EXCLUDED.enabled,expires_at=EXCLUDED.expires_at,updated_at=EXCLUDED.updated_at`,userID,key,enabled,expiresAt,time.Now().UTC());return err}
func (s *Store) Allowed(ctx context.Context,userID,key string)bool{var enabled bool;var expires time.Time;var hasExpires bool;err:=s.pool.QueryRow(ctx,`SELECT enabled,COALESCE(expires_at,'epoch'::timestamptz),expires_at IS NOT NULL FROM entitlements WHERE subject_type='user' AND subject_id=$1 AND entitlement=$2`,owner(userID),strings.TrimSpace(key)).Scan(&enabled,&expires,&hasExpires);if err!=nil||!enabled{return false};return !hasExpires||expires.After(time.Now())}

func (s *Store) EnrollDevice(ctx context.Context,identity domain.Identity,rawToken string)error{identity.UserID=owner(identity.UserID);identity.DeviceID=strings.TrimSpace(identity.DeviceID);if identity.DeviceID==""||len(rawToken)<16{return fmt.Errorf("user/device and token>=16 required")};hash:=sha256.Sum256([]byte(rawToken));now:=time.Now().UTC();_,err:=s.pool.Exec(ctx,`INSERT INTO device_credentials(device_id,user_id,tenant_id,plan,token_sha256,status,created_at,rotated_at) VALUES($1,$2,$3,$4,$5,'active',$6,$6) ON CONFLICT(device_id) DO UPDATE SET user_id=EXCLUDED.user_id,tenant_id=EXCLUDED.tenant_id,plan=EXCLUDED.plan,token_sha256=EXCLUDED.token_sha256,status='active',rotated_at=EXCLUDED.rotated_at`,identity.DeviceID,identity.UserID,strings.TrimSpace(identity.TenantID),strings.TrimSpace(identity.Plan),hex.EncodeToString(hash[:]),now);return err}
func (s *Store) AuthenticateDevice(ctx context.Context,deviceID,rawToken string)(domain.Identity,bool,error){deviceID=strings.TrimSpace(deviceID);hash:=sha256.Sum256([]byte(rawToken));identity:=domain.Identity{DeviceID:deviceID};var status,stored string;err:=s.pool.QueryRow(ctx,`SELECT user_id,tenant_id,plan,status,token_sha256 FROM device_credentials WHERE device_id=$1`,deviceID).Scan(&identity.UserID,&identity.TenantID,&identity.Plan,&status,&stored);if err==pgx.ErrNoRows{return identity,false,nil};if err!=nil{return identity,false,err};decoded,err:=hex.DecodeString(stored);if err!=nil{return identity,false,nil};return identity,status=="active"&&len(decoded)==sha256.Size&&subtle.ConstantTimeCompare(decoded,hash[:])==1,nil}
func (s *Store) RevokeDevice(ctx context.Context,deviceID string)error{tag,err:=s.pool.Exec(ctx,`UPDATE device_credentials SET status='revoked',rotated_at=$1 WHERE device_id=$2`,time.Now().UTC(),strings.TrimSpace(deviceID));if err!=nil{return err};return requireRowsChanged(tag.RowsAffected(),"device credential")}

func (s *Store) PutFirmware(ctx context.Context,manifest controlplane.FirmwareManifest,manifestJSON []byte)error{if !json.Valid(manifestJSON){return fmt.Errorf("firmware manifest_json must be valid JSON")};_,err:=s.pool.Exec(ctx,`INSERT INTO firmware_releases(metadata_version,version,channel,board,protocol_min,security_version,url,sha256,size,expires_at,signature,manifest_json,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::jsonb,$13) ON CONFLICT(metadata_version) DO UPDATE SET version=EXCLUDED.version,channel=EXCLUDED.channel,board=EXCLUDED.board,protocol_min=EXCLUDED.protocol_min,security_version=EXCLUDED.security_version,url=EXCLUDED.url,sha256=EXCLUDED.sha256,size=EXCLUDED.size,expires_at=EXCLUDED.expires_at,signature=EXCLUDED.signature,manifest_json=EXCLUDED.manifest_json`,manifest.MetadataVersion,manifest.Version,manifest.Channel,manifest.Board,manifest.ProtocolMin,manifest.SecurityVersion,manifest.URL,manifest.SHA256,manifest.Size,manifest.ExpiresAt.UTC(),manifest.Signature,string(manifestJSON),time.Now().UTC());return err}
func (s *Store) FirmwareCandidates(ctx context.Context,channel,board string)([]controlplane.StoredFirmware,error){rows,err:=s.pool.Query(ctx,`SELECT metadata_version,version,channel,board,protocol_min,security_version,url,sha256,size,expires_at,signature,manifest_json FROM firmware_releases WHERE channel=$1 AND board=$2 ORDER BY metadata_version DESC`,channel,board);if err!=nil{return nil,err};defer rows.Close();var out []controlplane.StoredFirmware;for rows.Next(){var stored controlplane.StoredFirmware;var raw []byte;if err:=rows.Scan(&stored.Manifest.MetadataVersion,&stored.Manifest.Version,&stored.Manifest.Channel,&stored.Manifest.Board,&stored.Manifest.ProtocolMin,&stored.Manifest.SecurityVersion,&stored.Manifest.URL,&stored.Manifest.SHA256,&stored.Manifest.Size,&stored.Manifest.ExpiresAt,&stored.Manifest.Signature,&raw);err!=nil{return nil,err};stored.Manifest.ExpiresAt=stored.Manifest.ExpiresAt.UTC();stored.JSON=append([]byte(nil),raw...);out=append(out,stored)};return out,rows.Err()}

func (s *Store) PutFeatureModule(ctx context.Context,module controlplane.FeatureModule)error{raw,err:=json.Marshal(module);if err!=nil{return err};_,err=s.pool.Exec(ctx,`INSERT INTO feature_modules(id,version,lifecycle,execution,manifest_json,updated_at) VALUES($1,$2,$3,$4,$5::jsonb,$6) ON CONFLICT(id) DO UPDATE SET version=EXCLUDED.version,lifecycle=EXCLUDED.lifecycle,execution=EXCLUDED.execution,manifest_json=EXCLUDED.manifest_json,updated_at=EXCLUDED.updated_at`,module.ID,module.Version,module.Lifecycle,module.Execution,string(raw),time.Now().UTC());return err}
func (s *Store) FeatureModules(ctx context.Context)([]controlplane.FeatureModule,error){rows,err:=s.pool.Query(ctx,`SELECT manifest_json FROM feature_modules ORDER BY id`);if err!=nil{return nil,err};defer rows.Close();var out []controlplane.FeatureModule;for rows.Next(){var raw []byte;if err:=rows.Scan(&raw);err!=nil{return nil,err};var module controlplane.FeatureModule;if err:=json.Unmarshal(raw,&module);err!=nil{return nil,err};out=append(out,module)};return out,rows.Err()}
func (s *Store) FeatureModule(ctx context.Context,id string)(controlplane.FeatureModule,bool,error){var raw []byte;err:=s.pool.QueryRow(ctx,`SELECT manifest_json FROM feature_modules WHERE id=$1`,strings.TrimSpace(id)).Scan(&raw);if err==pgx.ErrNoRows{return controlplane.FeatureModule{},false,nil};if err!=nil{return controlplane.FeatureModule{},false,err};var module controlplane.FeatureModule;if err:=json.Unmarshal(raw,&module);err!=nil{return controlplane.FeatureModule{},false,err};return module,true,nil}

var _ controlplane.Repository = (*Store)(nil)
var _ controlplane.ScopedRepository = (*Store)(nil)
var _ controlplane.FlagAdminRepository = (*Store)(nil)
var _ controlplane.FirmwareRepository = (*Store)(nil)
var _ controlplane.FeatureCatalogRepository = (*Store)(nil)
