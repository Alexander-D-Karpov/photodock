package develop

import (
	"encoding/binary"
	"math"
	"testing"
)

func fakeJPEGWithICC(chunks ...[]byte) []byte {
	out := []byte{0xFF, 0xD8}
	for i, c := range chunks {
		payload := append([]byte("ICC_PROFILE\x00"), byte(i+1), byte(len(chunks)))
		payload = append(payload, c...)
		seg := []byte{0xFF, 0xE2, 0, 0}
		binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
		out = append(out, seg...)
		out = append(out, payload...)
	}
	return append(out, 0xFF, 0xDA, 0, 2)
}

func TestJPEGICCConcatenatesChunks(t *testing.T) {
	data := fakeJPEGWithICC([]byte("abc"), []byte("def"))
	if got := string(extractICC(data)); got != "abcdef" {
		t.Errorf("icc = %q; want abcdef", got)
	}
	if extractICC([]byte{0xFF, 0xD8, 0xFF, 0xDA, 0, 2}) != nil {
		t.Error("jpeg without APP2 must have no icc")
	}
}

func s15(v float64) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, uint32(int32(math.Round(v*65536))))
	return b
}

func TestParseTRCPara(t *testing.T) {
	tag := append([]byte("para\x00\x00\x00\x00\x00\x00\x00\x00"), s15(2.2)...)
	f, err := parseTRC(tag)
	if err != nil {
		t.Fatal(err)
	}
	if got := f(0.5); math.Abs(got-math.Pow(0.5, 2.2)) > 1e-4 {
		t.Errorf("gamma 2.2 at 0.5 = %v", got)
	}
}

func TestParseTRCCurvTable(t *testing.T) {
	tag := []byte("curv\x00\x00\x00\x00\x00\x00\x00\x03")
	for _, v := range []uint16{0, 16384, 65535} {
		b := make([]byte, 2)
		binary.BigEndian.PutUint16(b, v)
		tag = append(tag, b...)
	}
	f, err := parseTRC(tag)
	if err != nil {
		t.Fatal(err)
	}
	if got := f(0.5); math.Abs(got-16384.0/65535) > 1e-6 {
		t.Errorf("curv(0.5) = %v", got)
	}
	if got := f(1); got != 1 {
		t.Errorf("curv(1) = %v", got)
	}
}

func TestParseICCRejectsGarbage(t *testing.T) {
	if _, err := parseICC([]byte("short")); err == nil {
		t.Error("expected error")
	}
	p := make([]byte, 200)
	copy(p[16:], "CMYK")
	if _, err := parseICC(p); err == nil {
		t.Error("CMYK must be rejected")
	}
}

func TestProfileForFallsBackToSRGB(t *testing.T) {
	prof, warn := profileFor([]byte{0xFF, 0xD8, 0xFF, 0xDA})
	if prof != srgbProfile || warn != "" {
		t.Error("no ICC must mean sRGB without warning")
	}
	prof, warn = profileFor(fakeJPEGWithICC([]byte("broken")))
	if prof != srgbProfile || warn == "" {
		t.Error("broken ICC must fall back to sRGB with a warning")
	}
}
