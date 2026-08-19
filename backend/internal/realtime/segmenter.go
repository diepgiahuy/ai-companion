package realtime

import (
	"strings"
	"unicode/utf8"
)

// Segmenter converts streamed text deltas into TTS-friendly clauses. It is
// deliberately deterministic: model/provider specific streaming stays outside
// the audio path and can be tested independently.
type Segmenter struct {
	buffer        strings.Builder
	first         bool
	minFirstRunes int
	minRunes      int
	maxRunes      int
}

// NewSegmenter returns defaults tuned for low-latency Vietnamese/English voice:
// the first clause may break earlier (including on a comma), while later
// clauses prefer stronger punctuation. MaxRunes is a safety valve for models
// that emit long punctuation-free text.
func NewSegmenter() *Segmenter {
	return &Segmenter{first: true, minFirstRunes: 8, minRunes: 12, maxRunes: 120}
}

// Push adds a text delta and returns zero or more complete TTS segments.
func (s *Segmenter) Push(delta string) []string {
	if delta == "" {
		return nil
	}
	s.buffer.WriteString(delta)
	return s.cut(false)
}

// Flush returns any remaining non-empty text at end-of-stream.
func (s *Segmenter) Flush() []string { return s.cut(true) }

func (s *Segmenter) cut(flush bool) []string {
	text := s.buffer.String()
	if strings.TrimSpace(text) == "" {
		if flush {
			s.buffer.Reset()
		}
		return nil
	}

	var out []string
	for {
		cut := s.findBoundary(text)
		if cut == 0 {
			if !flush {
				break
			}
			seg := strings.TrimSpace(text)
			if seg != "" {
				out = append(out, seg)
				s.first = false
			}
			text = ""
			break
		}
		seg := strings.TrimSpace(text[:cut])
		if seg != "" {
			out = append(out, seg)
			s.first = false
		}
		text = text[cut:]
		if strings.TrimSpace(text) == "" {
			text = ""
			break
		}
	}

	s.buffer.Reset()
	if text != "" {
		s.buffer.WriteString(text)
	}
	return out
}

func (s *Segmenter) findBoundary(text string) int {
	runes := 0
	for i, r := range text {
		runes++
		strong := isStrongPunctuation(r)
		medium := isMediumPunctuation(r)
		comma := isCommaPunctuation(r)
		min := s.minRunes
		if s.first {
			min = s.minFirstRunes
		}
		if strong && runes >= min {
			return i + utf8.RuneLen(r)
		}
		if medium && runes >= min {
			return i + utf8.RuneLen(r)
		}
		if s.first && comma && runes >= s.minFirstRunes {
			return i + utf8.RuneLen(r)
		}
		if runes >= s.maxRunes && (r == ' ' || strong || medium || comma) {
			return i + utf8.RuneLen(r)
		}
	}
	return 0
}

func isStrongPunctuation(r rune) bool {
	switch r {
	case '.', '!', '?', '。', '！', '？', '\n':
		return true
	default:
		return false
	}
}

func isMediumPunctuation(r rune) bool {
	switch r {
	case ';', '；', ':', '：':
		return true
	default:
		return false
	}
}

func isCommaPunctuation(r rune) bool {
	switch r {
	case ',', '，':
		return true
	default:
		return false
	}
}
