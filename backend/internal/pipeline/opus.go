//go:build !compilecheck

package pipeline

import (
	"encoding/binary"
	"fmt"
	"sync"

	"companion-server/internal/protocol"
	opus "gopkg.in/hraban/opus.v2"
)

// OpusFactory creates one stateful encoder/decoder pair per WebSocket session.
// The nolibopusfile build tag keeps the dependency limited to libopus.
type OpusFactory struct{}

func (OpusFactory) New() (AudioCodec, error) {
	decoder, err := opus.NewDecoder(protocol.UplinkSampleRate, protocol.Channels)
	if err != nil {
		return nil, fmt.Errorf("create uplink Opus decoder: %w", err)
	}
	encoder, err := opus.NewEncoder(protocol.DownlinkSampleRate, protocol.Channels, opus.AppVoIP)
	if err != nil {
		return nil, fmt.Errorf("create downlink Opus encoder: %w", err)
	}
	return &opusCodec{decoder: decoder, encoder: encoder}, nil
}

type opusCodec struct {
	decoder  *opus.Decoder
	encoder  *opus.Encoder
	decodeMu sync.Mutex
	encodeMu sync.Mutex
}

func (c *opusCodec) DecodeUplink(packet []byte) ([]byte, error) {
	c.decodeMu.Lock()
	defer c.decodeMu.Unlock()
	pcm := make([]int16, protocol.UplinkSamplesPerFrame)
	n, err := c.decoder.Decode(packet, pcm)
	if err != nil {
		return nil, fmt.Errorf("decode Opus: %w", err)
	}
	if n != protocol.UplinkSamplesPerFrame {
		return nil, fmt.Errorf("decoded %d samples; expected %d", n, protocol.UplinkSamplesPerFrame)
	}
	return int16ToBytes(pcm), nil
}

func (c *opusCodec) EncodeDownlink(raw []byte) ([]byte, error) {
	c.encodeMu.Lock()
	defer c.encodeMu.Unlock()
	if len(raw) != protocol.DownlinkSamplesPerFrame*2 {
		return nil, fmt.Errorf("TTS frame has %d PCM bytes; expected %d", len(raw), protocol.DownlinkSamplesPerFrame*2)
	}
	pcm := bytesToInt16(raw)
	packet := make([]byte, protocol.MaximumOpusPacketBytes)
	n, err := c.encoder.Encode(pcm, packet)
	if err != nil {
		return nil, fmt.Errorf("encode Opus: %w", err)
	}
	return packet[:n], nil
}

func int16ToBytes(samples []int16) []byte {
	result := make([]byte, len(samples)*2)
	for i, sample := range samples {
		binary.LittleEndian.PutUint16(result[i*2:], uint16(sample))
	}
	return result
}

func bytesToInt16(raw []byte) []int16 {
	result := make([]int16, len(raw)/2)
	for i := range result {
		result[i] = int16(binary.LittleEndian.Uint16(raw[i*2:]))
	}
	return result
}
