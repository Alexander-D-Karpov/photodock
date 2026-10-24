package develop

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
)

var errUnsupportedICC = errors.New("unsupported ICC profile")

type inputProfile struct {
	toRec2020 Mat3
	lut       [3][256]float32
}

var srgbProfile = newSRGBProfile()

func newSRGBProfile() *inputProfile {
	p := &inputProfile{toRec2020: rec2020FromSRGB}
	for c := 0; c < 3; c++ {
		for i := 0; i < 256; i++ {
			p.lut[c][i] = float32(srgbDecode(float64(i) / 255))
		}
	}
	return p
}

func newInputProfile(toXYZD50 Mat3, trc [3]func(float64) float64) *inputProfile {
	p := &inputProfile{toRec2020: rec2020FromXYZ.Mul(d65FromD50).Mul(toXYZD50)}
	for c := 0; c < 3; c++ {
		for i := 0; i < 256; i++ {
			v := trc[c](float64(i) / 255)
			if math.IsNaN(v) || v < 0 {
				v = 0
			}
			p.lut[c][i] = float32(v)
		}
	}
	return p
}

func profileFor(data []byte) (*inputProfile, string) {
	icc := extractICC(data)
	if len(icc) == 0 {
		return srgbProfile, ""
	}
	p, err := parseICC(icc)
	if err != nil {
		return srgbProfile, "ICC profile interpreted as sRGB: " + err.Error()
	}
	return p, ""
}

func extractICC(data []byte) []byte {
	if len(data) > 3 && data[0] == 0xFF && data[1] == 0xD8 {
		return jpegICC(data)
	}
	if len(data) > 8 && string(data[1:4]) == "PNG" {
		return pngICC(data)
	}
	return nil
}

func jpegICC(data []byte) []byte {
	type chunk struct {
		seq int
		b   []byte
	}
	var chunks []chunk
	pos := 2
	for pos+4 <= len(data) {
		if data[pos] != 0xFF {
			break
		}
		m := data[pos+1]
		if m == 0xFF {
			pos++
			continue
		}
		if m == 0x01 || (m >= 0xD0 && m <= 0xD7) {
			pos += 2
			continue
		}
		if m == 0xDA || m == 0xD9 {
			break
		}
		l := int(binary.BigEndian.Uint16(data[pos+2:]))
		end := pos + 2 + l
		if l < 2 || end > len(data) {
			break
		}
		seg := data[pos+4 : end]
		if m == 0xE2 && len(seg) > 14 && string(seg[:12]) == "ICC_PROFILE\x00" {
			chunks = append(chunks, chunk{seq: int(seg[12]), b: seg[14:]})
		}
		pos = end
	}
	if len(chunks) == 0 {
		return nil
	}
	sort.Slice(chunks, func(i, j int) bool { return chunks[i].seq < chunks[j].seq })
	var out []byte
	for _, c := range chunks {
		out = append(out, c.b...)
	}
	return out
}

func pngICC(data []byte) []byte {
	pos := 8
	for pos+8 <= len(data) {
		l := int(binary.BigEndian.Uint32(data[pos:]))
		typ := string(data[pos+4 : pos+8])
		end := pos + 8 + l
		if l < 0 || end > len(data) {
			return nil
		}
		switch typ {
		case "iCCP":
			body := data[pos+8 : end]
			i := bytes.IndexByte(body, 0)
			if i < 0 || i+2 > len(body) {
				return nil
			}
			zr, err := zlib.NewReader(bytes.NewReader(body[i+2:]))
			if err != nil {
				return nil
			}
			out, err := io.ReadAll(io.LimitReader(zr, 4<<20))
			_ = zr.Close()
			if err != nil {
				return nil
			}
			return out
		case "IDAT", "IEND":
			return nil
		}
		pos = end + 4
	}
	return nil
}

func s15f16(b []byte) float64 {
	return float64(int32(binary.BigEndian.Uint32(b))) / 65536
}

func parseICC(p []byte) (*inputProfile, error) {
	if len(p) < 132 {
		return nil, fmt.Errorf("%w: too short", errUnsupportedICC)
	}
	if string(p[16:20]) != "RGB " {
		return nil, fmt.Errorf("%w: color space %q", errUnsupportedICC, string(p[16:20]))
	}
	count := int(binary.BigEndian.Uint32(p[128:]))
	tags := make(map[string][]byte)
	for i := 0; i < count; i++ {
		o := 132 + i*12
		if o+12 > len(p) {
			break
		}
		sig := string(p[o : o+4])
		off := int(binary.BigEndian.Uint32(p[o+4:]))
		size := int(binary.BigEndian.Uint32(p[o+8:]))
		if off < 0 || size < 0 || off+size > len(p) {
			continue
		}
		tags[sig] = p[off : off+size]
	}
	var m Mat3
	for i, sig := range [3]string{"rXYZ", "gXYZ", "bXYZ"} {
		t := tags[sig]
		if len(t) < 20 || string(t[:4]) != "XYZ " {
			return nil, fmt.Errorf("%w: missing %s (LUT-based profile?)", errUnsupportedICC, sig)
		}
		m[0][i] = s15f16(t[8:])
		m[1][i] = s15f16(t[12:])
		m[2][i] = s15f16(t[16:])
	}
	var trc [3]func(float64) float64
	for i, sig := range [3]string{"rTRC", "gTRC", "bTRC"} {
		f, err := parseTRC(tags[sig])
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %w", errUnsupportedICC, sig, err)
		}
		trc[i] = f
	}
	return newInputProfile(m, trc), nil
}

var paraCounts = map[int]int{0: 1, 1: 3, 2: 4, 3: 5, 4: 7}

func parseTRC(t []byte) (func(float64) float64, error) {
	if len(t) < 12 {
		return nil, errors.New("missing curve")
	}
	switch string(t[:4]) {
	case "curv":
		n := int(binary.BigEndian.Uint32(t[8:]))
		if n == 0 {
			return func(x float64) float64 { return x }, nil
		}
		if len(t) < 12+2*n {
			return nil, errors.New("truncated curve")
		}
		if n == 1 {
			g := float64(binary.BigEndian.Uint16(t[12:])) / 256
			return func(x float64) float64 { return math.Pow(x, g) }, nil
		}
		tbl := make([]float64, n)
		for i := range tbl {
			tbl[i] = float64(binary.BigEndian.Uint16(t[12+2*i:])) / 65535
		}
		return func(x float64) float64 {
			x = clamp(x, 0, 1)
			pos := x * float64(n-1)
			i := int(pos)
			if i >= n-1 {
				return tbl[n-1]
			}
			f := pos - float64(i)
			return tbl[i]*(1-f) + tbl[i+1]*f
		}, nil
	case "para":
		typ := int(binary.BigEndian.Uint16(t[8:]))
		n, ok := paraCounts[typ]
		if !ok {
			return nil, fmt.Errorf("parametric type %d", typ)
		}
		if len(t) < 12+4*n {
			return nil, errors.New("truncated parametric curve")
		}
		var g [7]float64
		for i := 0; i < n; i++ {
			g[i] = s15f16(t[12+4*i:])
		}
		gamma, a, b, c, d, e, f := g[0], g[1], g[2], g[3], g[4], g[5], g[6]
		pw := func(v float64) float64 { return math.Pow(math.Max(v, 0), gamma) }
		switch typ {
		case 0:
			return func(x float64) float64 { return pw(x) }, nil
		case 1:
			return func(x float64) float64 {
				if x >= -b/a {
					return pw(a*x + b)
				}
				return 0
			}, nil
		case 2:
			return func(x float64) float64 {
				if x >= -b/a {
					return pw(a*x+b) + c
				}
				return c
			}, nil
		case 3:
			return func(x float64) float64 {
				if x >= d {
					return pw(a*x + b)
				}
				return c * x
			}, nil
		default:
			return func(x float64) float64 {
				if x >= d {
					return pw(a*x+b) + e
				}
				return c*x + f
			}, nil
		}
	}
	return nil, fmt.Errorf("curve type %q", string(t[:4]))
}
