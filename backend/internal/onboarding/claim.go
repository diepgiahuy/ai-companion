package onboarding

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"companion-server/internal/controlplane"
	"companion-server/internal/idempotency"
	"companion-server/internal/ownerauth"
)

const credentialDeliveryTTL = 10 * time.Minute

type ClaimAuthorizer interface { AuthorizeClaimAuthorization(string) (ownerauth.ClaimAuthorization, error) }

type ClaimService struct {
	repository controlplane.DeviceClaimRepository
	authorizer ClaimAuthorizer
	aead cipher.AEAD
	now func() time.Time
}

func NewClaimService(repository controlplane.DeviceClaimRepository, authorizer ClaimAuthorizer, key []byte) (*ClaimService, error) {
	if repository == nil || authorizer == nil { return nil, fmt.Errorf("device claim repository and authorizer are required") }
	if len(key) != 32 { return nil, fmt.Errorf("device claim delivery encryption key must be 32 bytes") }
	block, err := aes.NewCipher(key); if err != nil { return nil, err }
	aead, err := cipher.NewGCM(block); if err != nil { return nil, err }
	return &ClaimService{repository:repository, authorizer:authorizer, aead:aead, now:func() time.Time { return time.Now().UTC() }}, nil
}

func DecodeEncryptionKey(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" { return nil, fmt.Errorf("COMPANION_BOOTSTRAP_ENCRYPTION_KEY is required") }
	for _, decoder := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.RawURLEncoding} {
		if decoded, err := decoder.DecodeString(raw); err == nil && len(decoded) == 32 { return decoded, nil }
	}
	if decoded, err := hex.DecodeString(raw); err == nil && len(decoded) == 32 { return decoded, nil }
	return nil, fmt.Errorf("COMPANION_BOOTSTRAP_ENCRYPTION_KEY must decode to exactly 32 bytes")
}

func (s *ClaimService) Handler() http.Handler {
	mux := http.NewServeMux(); mux.HandleFunc("POST /v1/owner/device-claims", s.handleClaim); return mux
}

func (s *ClaimService) handleClaim(w http.ResponseWriter, r *http.Request) {
	claimToken, ok := bearerToken(r.Header.Get("Authorization")); if !ok { http.Error(w,"claim authorization required",http.StatusUnauthorized); return }
	idem := strings.TrimSpace(r.Header.Get("Idempotency-Key")); if len(idem)<8 || len(idem)>128 { http.Error(w,"valid Idempotency-Key required",http.StatusBadRequest); return }
	var req struct { DeviceID string `json:"device_id"`; BootstrapID string `json:"bootstrap_id"` }
	dec := json.NewDecoder(io.LimitReader(r.Body,8<<10)); dec.DisallowUnknownFields(); if err:=dec.Decode(&req); err!=nil { http.Error(w,"invalid request",http.StatusBadRequest); return }
	req.DeviceID=strings.TrimSpace(req.DeviceID); req.BootstrapID=strings.TrimSpace(req.BootstrapID)
	if req.DeviceID=="" || len(req.DeviceID)>128 || req.BootstrapID=="" || len(req.BootstrapID)>128 { http.Error(w,"invalid device_id/bootstrap_id",http.StatusBadRequest); return }
	auth, err := s.authorizer.AuthorizeClaimAuthorization(claimToken); if err!=nil || auth.BootstrapID!=req.BootstrapID { http.Error(w,"invalid claim authorization",http.StatusUnauthorized); return }
	resp, err := s.claim(r.Context(), auth.UserID, req.DeviceID, req.BootstrapID, idem)
	if err!=nil {
		switch { case errors.Is(err,controlplane.ErrDeviceAlreadyClaimed), idempotency.IsConflict(err): http.Error(w,err.Error(),http.StatusConflict); case errors.Is(err,controlplane.ErrClaimDeliveryUnavailable): http.Error(w,"claim delivery expired",http.StatusGone); default: http.Error(w,"device claim failed",http.StatusInternalServerError) }; return
	}
	w.Header().Set("Cache-Control","no-store"); w.Header().Set("Content-Type","application/json"); _=json.NewEncoder(w).Encode(resp)
}

type claimResponse struct { DeviceID string `json:"device_id"`; DeliveryID string `json:"delivery_id"`; DeviceCredential string `json:"device_credential"`; ExpiresAt time.Time `json:"expires_at"`; Replayed bool `json:"replayed"` }

func (s *ClaimService) claim(ctx context.Context, userID, deviceID, bootstrapID, key string) (claimResponse,error) {
	requestHash, err := idempotency.HashValue(struct{ DeviceID string `json:"device_id"`; BootstrapID string `json:"bootstrap_id"` }{deviceID,bootstrapID}); if err!=nil{return claimResponse{},err}
	raw, err := randomToken(32); if err!=nil{return claimResponse{},err}; deliveryID,err:=randomToken(18); if err!=nil{return claimResponse{},err}
	nonce:=make([]byte,s.aead.NonceSize()); if _,err:=rand.Read(nonce); err!=nil{return claimResponse{},err}; expires:=s.now().Add(credentialDeliveryTTL)
	ciphertext:=s.aead.Seal(nil,nonce,[]byte(raw),deliveryAAD(userID,deviceID,deliveryID)); digest:=sha256.Sum256([]byte(raw))
	outcome,err:=s.repository.ClaimDevice(ctx,controlplane.DeviceClaimMutation{UserID:userID,DeviceID:deviceID,CredentialHash:hex.EncodeToString(digest[:]),DeliveryID:deliveryID,CredentialCiphertext:ciphertext,CredentialNonce:nonce,ExpiresAt:expires,IdempotencyKey:key,RequestHash:requestHash}); if err!=nil{return claimResponse{},err}
	delivery,err:=s.repository.DeviceClaimDelivery(ctx,userID,outcome.DeliveryID); if err!=nil{return claimResponse{},err}
	plain,err:=s.aead.Open(nil,delivery.CredentialNonce,delivery.CredentialCiphertext,deliveryAAD(delivery.UserID,delivery.DeviceID,delivery.DeliveryID)); if err!=nil{return claimResponse{},fmt.Errorf("decrypt device claim delivery: %w",err)}
	return claimResponse{DeviceID:delivery.DeviceID,DeliveryID:delivery.DeliveryID,DeviceCredential:string(plain),ExpiresAt:delivery.ExpiresAt,Replayed:outcome.Replayed},nil
}

func bearerToken(raw string)(string,bool){parts:=strings.Fields(raw); if len(parts)==2&&strings.EqualFold(parts[0],"Bearer")&&strings.TrimSpace(parts[1])!=""{return strings.TrimSpace(parts[1]),true}; return "",false}
func randomToken(size int)(string,error){b:=make([]byte,size); if _,err:=rand.Read(b);err!=nil{return "",err}; return base64.RawURLEncoding.EncodeToString(b),nil}
func deliveryAAD(userID,deviceID,deliveryID string)[]byte{return []byte(strings.TrimSpace(userID)+"\x00"+strings.TrimSpace(deviceID)+"\x00"+strings.TrimSpace(deliveryID))}
