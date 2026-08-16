package assetbundle

import (
	"encoding/binary"
	"fmt"
)

func fontCovers(data []byte, required []rune) error {
	tables, err := sfntTables(data)
	if err != nil {
		return err
	}
	cmap, ok := tables["cmap"]
	if !ok {
		return fmt.Errorf("missing cmap")
	}
	cm := data[cmap.off : cmap.off+cmap.len]
	if len(cm) < 4 || binary.BigEndian.Uint16(cm[:2]) != 0 {
		return fmt.Errorf("invalid cmap")
	}
	n := int(binary.BigEndian.Uint16(cm[2:4]))
	if len(cm) < 4+8*n {
		return fmt.Errorf("truncated cmap")
	}
	var subs [][]byte
	for i := 0; i < n; i++ {
		rec := cm[4+i*8 : 12+i*8]
		platform := binary.BigEndian.Uint16(rec[:2])
		enc := binary.BigEndian.Uint16(rec[2:4])
		off := int(binary.BigEndian.Uint32(rec[4:8]))
		if off < 0 || off+2 > len(cm) {
			continue
		}
		if platform == 0 || (platform == 3 && (enc == 1 || enc == 10)) {
			subs = append(subs, cm[off:])
		}
	}
	if len(subs) == 0 {
		return fmt.Errorf("no Unicode cmap")
	}
	for _, r := range required {
		found := false
		for _, sub := range subs {
			ok, err := cmapHasRune(sub, r)
			if err == nil && ok {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("missing glyph U+%04X", r)
		}
	}
	return nil
}

type tableRange struct{ off, len int }

func sfntTables(data []byte) (map[string]tableRange, error) {
	if len(data) < 12 {
		return nil, fmt.Errorf("truncated sfnt")
	}
	ver := binary.BigEndian.Uint32(data[:4])
	if ver != 0x00010000 && ver != 0x4F54544F {
		return nil, fmt.Errorf("unsupported sfnt version")
	}
	n := int(binary.BigEndian.Uint16(data[4:6]))
	if n <= 0 || n > 128 || len(data) < 12+16*n {
		return nil, fmt.Errorf("invalid sfnt table directory")
	}
	out := map[string]tableRange{}
	for i := 0; i < n; i++ {
		r := data[12+i*16 : 28+i*16]
		tag := string(r[:4])
		off := int(binary.BigEndian.Uint32(r[8:12]))
		ln := int(binary.BigEndian.Uint32(r[12:16]))
		if off < 0 || ln < 0 || off > len(data) || ln > len(data)-off {
			return nil, fmt.Errorf("table %s out of bounds", tag)
		}
		out[tag] = tableRange{off, ln}
	}
	return out, nil
}
func cmapHasRune(sub []byte, r rune) (bool, error) {
	if len(sub) < 2 {
		return false, fmt.Errorf("short cmap")
	}
	switch binary.BigEndian.Uint16(sub[:2]) {
	case 4:
		return cmap4Has(sub, r)
	case 12:
		return cmap12Has(sub, r)
	default:
		return false, nil
	}
}
func cmap12Has(sub []byte, r rune) (bool, error) {
	if len(sub) < 16 {
		return false, fmt.Errorf("short cmap12")
	}
	length := int(binary.BigEndian.Uint32(sub[4:8]))
	groups := int(binary.BigEndian.Uint32(sub[12:16]))
	if length > len(sub) || groups < 0 || 16+groups*12 > length {
		return false, fmt.Errorf("invalid cmap12")
	}
	cp := uint32(r)
	lo, hi := 0, groups
	for lo < hi {
		mid := (lo + hi) / 2
		g := sub[16+mid*12 : 28+mid*12]
		start, end := binary.BigEndian.Uint32(g[:4]), binary.BigEndian.Uint32(g[4:8])
		if cp < start {
			hi = mid
		} else if cp > end {
			lo = mid + 1
		} else {
			return binary.BigEndian.Uint32(g[8:12])+(cp-start) != 0, nil
		}
	}
	return false, nil
}
func cmap4Has(sub []byte, r rune) (bool, error) {
	if r < 0 || r > 0xffff || len(sub) < 16 {
		return false, nil
	}
	length := int(binary.BigEndian.Uint16(sub[2:4]))
	segCount := int(binary.BigEndian.Uint16(sub[6:8])) / 2
	if segCount <= 0 || length > len(sub) {
		return false, fmt.Errorf("invalid cmap4")
	}
	endOff := 14
	startOff := endOff + 2*segCount + 2
	deltaOff := startOff + 2*segCount
	rangeOff := deltaOff + 2*segCount
	if rangeOff+2*segCount > length {
		return false, fmt.Errorf("truncated cmap4")
	}
	cp := uint16(r)
	for i := 0; i < segCount; i++ {
		end := binary.BigEndian.Uint16(sub[endOff+2*i:])
		start := binary.BigEndian.Uint16(sub[startOff+2*i:])
		if cp < start || cp > end {
			continue
		}
		delta := int16(binary.BigEndian.Uint16(sub[deltaOff+2*i:]))
		ro := binary.BigEndian.Uint16(sub[rangeOff+2*i:])
		var glyph uint16
		if ro == 0 {
			glyph = uint16(int(cp) + int(delta))
		} else {
			pos := rangeOff + 2*i + int(ro) + 2*int(cp-start)
			if pos+2 > length {
				return false, fmt.Errorf("cmap4 glyph offset out of bounds")
			}
			glyph = binary.BigEndian.Uint16(sub[pos:])
			if glyph != 0 {
				glyph = uint16(int(glyph) + int(delta))
			}
		}
		return glyph != 0, nil
	}
	return false, nil
}
