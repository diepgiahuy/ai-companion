//go:build compilecheck

package pipeline

import "fmt"

type OpusFactory struct{}

func (OpusFactory) New() (AudioCodec, error) { return compileCodec{}, nil }

type compileCodec struct{}

func (compileCodec) DecodeUplink(packet []byte) ([]byte, error) {
	if len(packet) == 0 {
		return nil, fmt.Errorf("empty packet")
	}
	return append([]byte(nil), packet...), nil
}
func (compileCodec) EncodeDownlink(pcm []byte) ([]byte, error) {
	if len(pcm) == 0 {
		return nil, fmt.Errorf("empty pcm")
	}
	return append([]byte(nil), pcm...), nil
}
