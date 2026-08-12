//go:build !webrtc

package webrtcbridge

func newBridge(Config) (Bridge, error) { return nil, ErrNotBuilt }
