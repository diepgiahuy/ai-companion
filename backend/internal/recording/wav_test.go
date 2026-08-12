package recording

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestWritePCM16MonoWAV(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memo.wav")
	pcm := make([]byte, 320)
	if err := WritePCM16MonoWAV(path, pcm, 16000); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" || string(data[36:40]) != "data" {
		t.Fatalf("invalid wav header: %q %q %q", data[:4], data[8:12], data[36:40])
	}
	if got := binary.LittleEndian.Uint32(data[24:28]); got != 16000 {
		t.Fatalf("sample rate = %d", got)
	}
	if got := binary.LittleEndian.Uint32(data[40:44]); got != uint32(len(pcm)) {
		t.Fatalf("data size = %d", got)
	}
}
