package webrtcbridge

import (
	"context"
	"errors"
	"time"
)

var ErrNotBuilt = errors.New("WebRTC integration is not included in this build; rebuild with -tags=webrtc")

type Config struct {
	ICEServers        []string
	OpusFrameDuration time.Duration
}

type IncomingOpus struct {
	PeerID    string
	Payload   []byte
	Timestamp uint32
	Sequence  uint16
}

type OpusHandler func(context.Context, IncomingOpus) error

type Bridge interface {
	HandleOffer(ctx context.Context, offerSDP string, onOpus OpusHandler) (peerID, answerSDP string, err error)
	WriteOpus(ctx context.Context, peerID string, payload []byte, duration time.Duration) error
	ClosePeer(peerID string) error
	Close() error
}

func New(config Config) (Bridge, error) {
	return newBridge(config)
}
