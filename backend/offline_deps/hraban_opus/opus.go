package opus

/*
#cgo LDFLAGS: -l:libopus.so.0
#include <stdint.h>
#include <stdlib.h>
typedef struct OpusEncoder OpusEncoder;
typedef struct OpusDecoder OpusDecoder;
OpusEncoder *opus_encoder_create(int Fs, int channels, int application, int *error);
void opus_encoder_destroy(OpusEncoder *st);
int opus_encode(OpusEncoder *st, const int16_t *pcm, int frame_size, unsigned char *data, int max_data_bytes);
OpusDecoder *opus_decoder_create(int Fs, int channels, int *error);
void opus_decoder_destroy(OpusDecoder *st);
int opus_decode(OpusDecoder *st, const unsigned char *data, int len, int16_t *pcm, int frame_size, int decode_fec);
const char *opus_strerror(int error);
*/
import "C"
import (
	"fmt"
	"runtime"
	"unsafe"
)

type Application int

const AppVoIP Application = 2048

type Encoder struct {
	p        *C.OpusEncoder
	channels int
}

func NewEncoder(rate, channels int, app Application) (*Encoder, error) {
	var code C.int
	p := C.opus_encoder_create(C.int(rate), C.int(channels), C.int(app), &code)
	if p == nil || code != 0 {
		return nil, fmt.Errorf("opus encoder: %s", C.GoString(C.opus_strerror(code)))
	}
	e := &Encoder{p: p, channels: channels}
	runtime.SetFinalizer(e, func(x *Encoder) {
		if x.p != nil {
			C.opus_encoder_destroy(x.p)
			x.p = nil
		}
	})
	return e, nil
}
func (e *Encoder) Encode(pcm []int16, out []byte) (int, error) {
	if e == nil || e.p == nil {
		return 0, fmt.Errorf("opus encoder closed")
	}
	if len(pcm) == 0 || len(out) == 0 {
		return 0, fmt.Errorf("empty opus input")
	}
	frame := len(pcm) / e.channels
	n := C.opus_encode(e.p, (*C.int16_t)(unsafe.Pointer(&pcm[0])), C.int(frame), (*C.uchar)(unsafe.Pointer(&out[0])), C.int(len(out)))
	runtime.KeepAlive(e)
	if n < 0 {
		return 0, fmt.Errorf("opus encode: %s", C.GoString(C.opus_strerror(n)))
	}
	return int(n), nil
}

type Decoder struct {
	p        *C.OpusDecoder
	channels int
}

func NewDecoder(rate, channels int) (*Decoder, error) {
	var code C.int
	p := C.opus_decoder_create(C.int(rate), C.int(channels), &code)
	if p == nil || code != 0 {
		return nil, fmt.Errorf("opus decoder: %s", C.GoString(C.opus_strerror(code)))
	}
	d := &Decoder{p: p, channels: channels}
	runtime.SetFinalizer(d, func(x *Decoder) {
		if x.p != nil {
			C.opus_decoder_destroy(x.p)
			x.p = nil
		}
	})
	return d, nil
}
func (d *Decoder) Decode(data []byte, pcm []int16) (int, error) {
	if d == nil || d.p == nil {
		return 0, fmt.Errorf("opus decoder closed")
	}
	if len(pcm) == 0 {
		return 0, fmt.Errorf("empty pcm buffer")
	}
	var dp *C.uchar
	if len(data) > 0 {
		dp = (*C.uchar)(unsafe.Pointer(&data[0]))
	}
	frame := len(pcm) / d.channels
	n := C.opus_decode(d.p, dp, C.int(len(data)), (*C.int16_t)(unsafe.Pointer(&pcm[0])), C.int(frame), 0)
	runtime.KeepAlive(d)
	if n < 0 {
		return 0, fmt.Errorf("opus decode: %s", C.GoString(C.opus_strerror(n)))
	}
	return int(n), nil
}
