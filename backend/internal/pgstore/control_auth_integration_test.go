package pgstore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"companion-server/internal/controlplane"
	conversationctx "companion-server/internal/conversation"
	"companion-server/internal/domain"
)

func TestPostgresConversationControlAndAuthParity(t *testing.T) {
	pool := postgresTestPool(t)
	store, err := New(pool)
	if err != nil { t.Fatal(err) }
	ctx := context.Background()
	prefix := fmt.Sprintf("pg-state-%d", time.Now().UnixNano())
	user := prefix + "-user"
	device := prefix + "-device"

	scope := conversationctx.Scope{UserID:user,ThreadID:"default"}
	if err:=store.Append(ctx,prefix+"-turn",scope,"user","hello");err!=nil{t.Fatal(err)}
	if err:=store.Append(ctx,prefix+"-turn",scope,"user","hello");err!=nil{t.Fatal(err)}
	if err:=store.Append(ctx,prefix+"-turn",scope,"assistant","hi");err!=nil{t.Fatal(err)}
	messages,err:=store.Recent(ctx,scope,10);if err!=nil||len(messages)!=2{t.Fatalf("messages=%+v err=%v",messages,err)}

	twin,err:=store.GetTwin(ctx,user,device);if err!=nil{t.Fatal(err)}
	if twin.DeviceID!=device||twin.UserID!=user{t.Fatalf("twin=%+v",twin)}
	locale:="vi-VN";twin.Desired=controlplane.RuntimeConfig{Locale:locale};twin.DesiredVersion=1;twin.UpdatedAt=time.Now().UTC()
	if err:=store.SaveTwin(ctx,twin);err!=nil{t.Fatal(err)}
	reloaded,err:=store.GetTwin(ctx,user,device);if err!=nil||reloaded.Desired.Locale!=locale||reloaded.DesiredVersion!=1{t.Fatalf("twin=%+v err=%v",reloaded,err)}
	threshold:=500
	if err:=store.SetScopedConfig(ctx,"user",user,controlplane.RuntimeConfig{VADThreshold:&threshold});err!=nil{t.Fatal(err)}
	override,ok,err:=store.GetScopedConfig(ctx,"user",user);if err!=nil||!ok||override.Config.VADThreshold==nil||*override.Config.VADThreshold!=500{t.Fatalf("override=%+v ok=%v err=%v",override,ok,err)}
	before,err:=store.ConfigGeneration(ctx);if err!=nil{t.Fatal(err)};after,err:=store.BumpGeneration(ctx);if err!=nil||after!=before+1{t.Fatalf("generation before=%d after=%d err=%v",before,after,err)}

	flag:=controlplane.Flag{Key:prefix+"-flag",Enabled:true,Rollout:100,Lifecycle:"released",Variants:map[string]string{"mode":"test"}}
	if err:=store.SetFlag(ctx,flag);err!=nil{t.Fatal(err)}
	flags,err:=store.Flags(ctx);if err!=nil{t.Fatal(err)};foundFlag:=false;for _,f:=range flags{if f.Key==flag.Key{foundFlag=f.Enabled&&f.Variants["mode"]=="test"}};if !foundFlag{t.Fatalf("flag not found: %+v",flags)}

	identity:=domain.Identity{UserID:user,DeviceID:device,TenantID:"tenant-a",Plan:"test"}
	token:="0123456789abcdef0123456789abcdef"
	if err:=store.EnrollDevice(ctx,identity,token);err!=nil{t.Fatal(err)}
	auth,ok,err:=store.AuthenticateDevice(ctx,device,token);if err!=nil||!ok||auth.UserID!=user||auth.TenantID!="tenant-a"||auth.Plan!="test"{t.Fatalf("auth=%+v ok=%v err=%v",auth,ok,err)}
	if _,ok,err:=store.AuthenticateDevice(ctx,device,"wrong-wrong-wrong-token");err!=nil||ok{t.Fatalf("wrong credential ok=%v err=%v",ok,err)}
	if err:=store.SetEntitlement(ctx,user,"capability.test",true,nil);err!=nil{t.Fatal(err)}
	if !store.Allowed(ctx,user,"capability.test"){t.Fatal("enabled entitlement denied")}
	if err:=store.RevokeDevice(ctx,device);err!=nil{t.Fatal(err)}
	if _,ok,err:=store.AuthenticateDevice(ctx,device,token);err!=nil||ok{t.Fatalf("revoked credential ok=%v err=%v",ok,err)}
}

func TestPostgresFirmwareAndModuleParity(t *testing.T){
	pool:=postgresTestPool(t);store,err:=New(pool);if err!=nil{t.Fatal(err)};ctx:=context.Background();prefix:=fmt.Sprintf("pg-fw-%d",time.Now().UnixNano())
	manifest:=controlplane.FirmwareManifest{MetadataVersion:time.Now().UnixNano(),Version:"1.2.3",Channel:prefix,Board:"esp32s3",ProtocolMin:2,SecurityVersion:1,URL:"https://example.invalid/fw.bin",SHA256:"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",Size:123,ExpiresAt:time.Now().Add(time.Hour).UTC(),Signature:"test"}
	if err:=store.PublishFirmware(ctx,manifest);err!=nil{t.Fatal(err)}
	manifests,err:=store.LatestFirmware(ctx,prefix,"esp32s3");if err!=nil||len(manifests)!=1||manifests[0].MetadataVersion!=manifest.MetadataVersion{t.Fatalf("manifests=%+v err=%v",manifests,err)}
	module:=controlplane.FeatureModule{ID:prefix+".module",Version:1,Lifecycle:"beta",Execution:"native",Implementation:"test"}
	if err:=store.PutFeatureModule(ctx,module);err!=nil{t.Fatal(err)}
	modules,err:=store.FeatureModules(ctx);if err!=nil{t.Fatal(err)};found:=false;for _,m:=range modules{if m.ID==module.ID{found=m.Version==1&&m.Implementation=="test"}};if !found{t.Fatalf("module not found: %+v",modules)}
}
