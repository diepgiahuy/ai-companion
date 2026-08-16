//go:build apppge2e

package pgstore_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"companion-server/internal/domain"
	"companion-server/internal/pgstore"
	"companion-server/internal/pipeline"
	"companion-server/internal/privacy"
	"companion-server/internal/server"
	"companion-server/internal/voicemail"
)

func TestVoiceMailAuthenticatedApplicationLifecycle(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("COMPANION_POSTGRES_APP_TEST_DSN"))
	if dsn == "" {
		t.Skip("COMPANION_POSTGRES_APP_TEST_DSN is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	data, err := pgstore.OpenStore(ctx, pgstore.PoolConfig{DSN: dsn, AllowInsecureRemote: true})
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	prefix := fmt.Sprintf("vm-app-%d", time.Now().UnixNano())
	senderUser, recipientUser := prefix+"-sender", prefix+"-recipient"
	senderDevice, recipientDevice := prefix+"-sender-device", prefix+"-recipient-device"
	senderToken, recipientToken := prefix+"-sender-token-long-enough", prefix+"-recipient-token-long-enough"
	for _, p := range []privacy.Policy{{UserID: senderUser, VoiceMailPolicy: "ephemeral"}, {UserID: recipientUser, VoiceMailPolicy: "ephemeral"}} {
		if err := data.SetPrivacyPolicy(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	if err := data.EnrollDevice(ctx, domain.Identity{UserID: senderUser, DeviceID: senderDevice}, senderToken); err != nil {
		t.Fatal(err)
	}
	if err := data.EnrollDevice(ctx, domain.Identity{UserID: recipientUser, DeviceID: recipientDevice}, recipientToken); err != nil {
		t.Fatal(err)
	}

	relID := prefix + "-app-rel"
	devA, userA, devB, userB := senderDevice, senderUser, recipientDevice, recipientUser
	if devB < devA {
		devA, devB = devB, devA
		userA, userB = userB, userA
	}
	if err := data.InsertRelationshipForTest(ctx, relID, devA, devB, userA, userB); err != nil {
		t.Fatal(err)
	}

	blobs, err := voicemail.NewFileSystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mail, err := voicemail.New(data, blobs)
	if err != nil {
		t.Fatal(err)
	}
	app := server.New(pipeline.Components{}, nil, server.WithDeviceAuthenticator(data), server.WithVoiceMail(mail))
	httpServer := httptest.NewServer(app.Handler())
	defer func() { httpServer.Close() }()

	var recipientsResp struct {
		Recipients []struct {
			RelationshipID string `json:"relationship_id"`
			PeerDeviceID   string `json:"peer_device_id"`
		} `json:"recipients"`
	}
	doVoiceMailJSON(t, httpServer.URL, "GET", "/v1/voice-mail/recipients", senderDevice, senderToken, nil, http.StatusOK, &recipientsResp)
	if len(recipientsResp.Recipients) != 1 || recipientsResp.Recipients[0].RelationshipID != relID || recipientsResp.Recipients[0].PeerDeviceID != recipientDevice {
		t.Fatalf("recipients=%+v", recipientsResp)
	}

	media := []byte("synthetic ogg opus application fixture")
	sum := sha256.Sum256(media)
	checksum := hex.EncodeToString(sum[:])
	create := map[string]any{
		"relationship_id": relID,
		"duration_ms":     1200,
		"size_bytes":      len(media),
		"checksum_sha256": checksum,
		"policy":          "ephemeral",
		"expires_at":      time.Now().Add(time.Hour).UTC(),
		"idempotency_key": prefix + "-create",
	}
	created := voicemail.Item{}
	doVoiceMailJSON(t, httpServer.URL, "POST", "/v1/voice-mail", senderDevice, senderToken, create, http.StatusCreated, &created)
	if created.ID == "" || created.RelationshipID != relID || created.State != voicemail.PendingUpload || created.ObjectKey != "" {
		t.Fatalf("created=%+v", created)
	}

	put, _ := http.NewRequest(http.MethodPut, httpServer.URL+"/v1/voice-mail/"+created.ID+"/media", bytes.NewReader(media))
	authorizeVoiceMail(put, senderDevice, senderToken)
	response, err := http.DefaultClient.Do(put)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("upload status=%d", response.StatusCode)
	}
	var completed voicemail.Item
	doVoiceMailJSON(t, httpServer.URL, "POST", "/v1/voice-mail/"+created.ID+"/complete", senderDevice, senderToken, map[string]string{"idempotency_key": prefix + "-complete"}, http.StatusOK, &completed)
	doVoiceMailJSON(t, httpServer.URL, "POST", "/v1/voice-mail/"+created.ID+"/complete", senderDevice, senderToken, map[string]string{"idempotency_key": prefix + "-complete"}, http.StatusOK, &completed)
	httpServer.Close()
	httpServer = httptest.NewServer(server.New(pipeline.Components{}, nil, server.WithDeviceAuthenticator(data), server.WithVoiceMail(mail)).Handler())

	var listed struct {
		Items []voicemail.Item `json:"items"`
	}
	doVoiceMailJSON(t, httpServer.URL, "GET", "/v1/voice-mail", recipientDevice, recipientToken, nil, http.StatusOK, &listed)
	if len(listed.Items) != 1 || listed.Items[0].ID != created.ID {
		t.Fatalf("listed=%+v", listed.Items)
	}
	var claimed voicemail.Item
	doVoiceMailJSON(t, httpServer.URL, "POST", "/v1/voice-mail/"+created.ID+"/claim", recipientDevice, recipientToken, map[string]string{"playback_id": "play-1", "idempotency_key": prefix + "-claim"}, http.StatusOK, &claimed)

	get, _ := http.NewRequest(http.MethodGet, httpServer.URL+"/v1/voice-mail/"+created.ID+"/media?playback_id=play-1", nil)
	authorizeVoiceMail(get, recipientDevice, recipientToken)
	response, err = http.DefaultClient.Do(get)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !bytes.Equal(got, media) {
		t.Fatalf("media status=%d got=%q", response.StatusCode, got)
	}
	doVoiceMailJSON(t, httpServer.URL, "POST", "/v1/voice-mail/"+created.ID+"/playback", recipientDevice, recipientToken, map[string]string{"playback_id": "play-1", "result": "succeeded", "idempotency_key": prefix + "-playback"}, http.StatusOK, &claimed)

	getAfter, _ := http.NewRequest(http.MethodGet, httpServer.URL+"/v1/voice-mail/"+created.ID+"/media?playback_id=play-1", nil)
	authorizeVoiceMail(getAfter, recipientDevice, recipientToken)
	response, err = http.DefaultClient.Do(getAfter)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode == http.StatusOK {
		t.Fatal("ephemeral media remained accessible")
	}

	unauthorized, _ := http.NewRequest(http.MethodGet, httpServer.URL+"/v1/voice-mail", nil)
	authorizeVoiceMail(unauthorized, recipientDevice, "wrong-token")
	response, err = http.DefaultClient.Do(unauthorized)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong credential status=%d", response.StatusCode)
	}
}

func doVoiceMailJSON(t *testing.T, baseURL, method, path, deviceID, token string, body any, want int, target any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, baseURL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	authorizeVoiceMail(request, deviceID, token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != want {
		raw, _ := io.ReadAll(response.Body)
		t.Fatalf("%s %s status=%d want=%d body=%s", method, path, response.StatusCode, want, raw)
	}
	if target != nil {
		if err := json.NewDecoder(response.Body).Decode(target); err != nil {
			t.Fatal(err)
		}
	}
}

func authorizeVoiceMail(request *http.Request, deviceID, token string) {
	request.Header.Set("Device-Id", deviceID)
	request.Header.Set("Authorization", "Bearer "+token)
}
