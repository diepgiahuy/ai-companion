//go:build webrtc

package webrtcbridge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

type peer struct {
	pc    *webrtc.PeerConnection
	audio *webrtc.TrackLocalStaticSample
}

type bridge struct {
	api      *webrtc.API
	config   webrtc.Configuration
	frameDur time.Duration
	mu       sync.RWMutex
	peers    map[string]*peer
}

func newBridge(config Config) (Bridge, error) {
	mediaEngine := &webrtc.MediaEngine{}
	if err := mediaEngine.RegisterDefaultCodecs(); err != nil {
		return nil, fmt.Errorf("register WebRTC codecs: %w", err)
	}
	settingEngine := webrtc.SettingEngine{}
	api := webrtc.NewAPI(webrtc.WithMediaEngine(mediaEngine), webrtc.WithSettingEngine(settingEngine))
	ice := make([]webrtc.ICEServer, 0, len(config.ICEServers))
	for _, endpoint := range config.ICEServers {
		endpoint = strings.TrimSpace(endpoint)
		if endpoint != "" {
			ice = append(ice, webrtc.ICEServer{URLs: []string{endpoint}})
		}
	}
	frameDur := config.OpusFrameDuration
	if frameDur <= 0 {
		frameDur = 20 * time.Millisecond
	}
	if frameDur < 10*time.Millisecond || frameDur > 60*time.Millisecond {
		return nil, fmt.Errorf("WebRTC Opus frame duration must be between 10ms and 60ms")
	}
	return &bridge{api: api, config: webrtc.Configuration{ICEServers: ice}, frameDur: frameDur, peers: map[string]*peer{}}, nil
}

func (b *bridge) HandleOffer(ctx context.Context, offerSDP string, onOpus OpusHandler) (string, string, error) {
	if strings.TrimSpace(offerSDP) == "" {
		return "", "", fmt.Errorf("WebRTC offer SDP is required")
	}
	if onOpus == nil {
		return "", "", fmt.Errorf("incoming Opus handler is required")
	}
	pc, err := b.api.NewPeerConnection(b.config)
	if err != nil {
		return "", "", fmt.Errorf("create peer connection: %w", err)
	}
	track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{
		MimeType:  webrtc.MimeTypeOpus,
		ClockRate: 48000,
		Channels:  1,
	}, "audio", "companion")
	if err != nil {
		_ = pc.Close()
		return "", "", fmt.Errorf("create local Opus track: %w", err)
	}
	if _, err := pc.AddTrack(track); err != nil {
		_ = pc.Close()
		return "", "", fmt.Errorf("add local Opus track: %w", err)
	}
	peerID, err := randomPeerID()
	if err != nil {
		_ = pc.Close()
		return "", "", err
	}
	pc.OnTrack(func(remote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		if !strings.EqualFold(remote.Codec().MimeType, webrtc.MimeTypeOpus) {
			return
		}
		for {
			packet, _, readErr := remote.ReadRTP()
			if readErr != nil {
				return
			}
			payload := append([]byte(nil), packet.Payload...)
			if err := onOpus(ctx, IncomingOpus{PeerID: peerID, Payload: payload, Timestamp: packet.Timestamp, Sequence: packet.SequenceNumber}); err != nil {
				return
			}
		}
	})
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
			_ = b.ClosePeer(peerID)
		}
	})
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offerSDP}); err != nil {
		_ = pc.Close()
		return "", "", fmt.Errorf("set WebRTC remote description: %w", err)
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		_ = pc.Close()
		return "", "", fmt.Errorf("create WebRTC answer: %w", err)
	}
	gatherComplete := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		_ = pc.Close()
		return "", "", fmt.Errorf("set WebRTC local description: %w", err)
	}
	select {
	case <-ctx.Done():
		_ = pc.Close()
		return "", "", ctx.Err()
	case <-gatherComplete:
	}
	local := pc.LocalDescription()
	if local == nil || strings.TrimSpace(local.SDP) == "" {
		_ = pc.Close()
		return "", "", fmt.Errorf("WebRTC local description is unavailable")
	}
	b.mu.Lock()
	b.peers[peerID] = &peer{pc: pc, audio: track}
	b.mu.Unlock()
	return peerID, local.SDP, nil
}

func (b *bridge) WriteOpus(ctx context.Context, peerID string, payload []byte, duration time.Duration) error {
	if len(payload) == 0 {
		return fmt.Errorf("Opus payload is empty")
	}
	b.mu.RLock()
	p := b.peers[peerID]
	b.mu.RUnlock()
	if p == nil {
		return fmt.Errorf("WebRTC peer %q not found", peerID)
	}
	if duration <= 0 {
		duration = b.frameDur
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if err := p.audio.WriteSample(media.Sample{Data: append([]byte(nil), payload...), Duration: duration}); err != nil {
		return fmt.Errorf("write WebRTC Opus sample: %w", err)
	}
	return nil
}

func (b *bridge) ClosePeer(peerID string) error {
	b.mu.Lock()
	p := b.peers[peerID]
	delete(b.peers, peerID)
	b.mu.Unlock()
	if p == nil {
		return nil
	}
	return p.pc.Close()
}

func (b *bridge) Close() error {
	b.mu.Lock()
	peers := b.peers
	b.peers = map[string]*peer{}
	b.mu.Unlock()
	var first error
	for _, p := range peers {
		if err := p.pc.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func randomPeerID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate WebRTC peer id: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}
