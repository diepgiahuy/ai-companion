package recording

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
)

// WritePCM16MonoWAV atomically writes signed little-endian 16-bit mono PCM as a
// standards-compliant RIFF/WAVE file. The caller owns naming/idempotency.
func WritePCM16MonoWAV(path string, pcm []byte, sampleRate int) error {
	if len(pcm) == 0 || len(pcm)%2 != 0 {
		return fmt.Errorf("pcm must contain non-empty 16-bit samples")
	}
	if sampleRate <= 0 || sampleRate > 192000 {
		return fmt.Errorf("invalid sample rate %d", sampleRate)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".voice-memo-*.wav.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpName)
	}

	dataSize := uint32(len(pcm))
	byteRate := uint32(sampleRate * 2)
	header := make([]byte, 44)
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], 36+dataSize)
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 1) // PCM
	binary.LittleEndian.PutUint16(header[22:24], 1) // mono
	binary.LittleEndian.PutUint32(header[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(header[28:32], byteRate)
	binary.LittleEndian.PutUint16(header[32:34], 2)
	binary.LittleEndian.PutUint16(header[34:36], 16)
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], dataSize)
	if _, err := tmp.Write(header); err != nil {
		cleanup()
		return err
	}
	if _, err := tmp.Write(pcm); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}
