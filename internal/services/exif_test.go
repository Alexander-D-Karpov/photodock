package services

import "testing"

func TestGetString(t *testing.T) {
	data := map[string]interface{}{
		"str":    "  hello  ",
		"intish": float64(4),
		"frac":   float64(1.5),
		"native": 7,
	}
	cases := map[string]string{
		"str":     "hello", // trimmed
		"intish":  "4",     // whole float renders without decimals
		"frac":    "1.50",  // fractional float keeps 2 decimals
		"native":  "7",
		"missing": "",
	}
	for key, want := range cases {
		if got := getString(data, key); got != want {
			t.Errorf("getString(%q) = %q; want %q", key, got, want)
		}
	}
}

func TestGetFloat(t *testing.T) {
	data := map[string]interface{}{
		"f":   float64(2.8),
		"s":   "3.5",
		"bad": "not-a-number",
	}
	if got := getFloat(data, "f"); got != 2.8 {
		t.Errorf("getFloat(f) = %v; want 2.8", got)
	}
	if got := getFloat(data, "s"); got != 3.5 {
		t.Errorf("getFloat(s) = %v; want 3.5", got)
	}
	if got := getFloat(data, "bad"); got != 0 {
		t.Errorf("getFloat(bad) = %v; want 0", got)
	}
	if got := getFloat(data, "missing"); got != 0 {
		t.Errorf("getFloat(missing) = %v; want 0", got)
	}
}

func TestGetInt(t *testing.T) {
	data := map[string]interface{}{
		"f": float64(6),
		"s": "8",
		"n": 100,
	}
	cases := map[string]int{"f": 6, "s": 8, "n": 100, "missing": 0}
	for key, want := range cases {
		if got := getInt(data, key); got != want {
			t.Errorf("getInt(%q) = %d; want %d", key, got, want)
		}
	}
}

func TestCleanString(t *testing.T) {
	if got := cleanString("Canon\x00\x00"); got != "Canon" {
		t.Errorf("cleanString trailing nulls = %q; want %q", got, "Canon")
	}
	if got := cleanString("  spaced  "); got != "spaced" {
		t.Errorf("cleanString spaces = %q; want %q", got, "spaced")
	}
}

func TestDecodeFlash(t *testing.T) {
	cases := map[int]string{
		0x00: "Did not fire",
		0x01: "Fired",
		0x09: "Fired, compulsory", // fired + mode 1
		0x18: "Did not fire, auto",
	}
	for val, want := range cases {
		if got := decodeFlash(val); got != want {
			t.Errorf("decodeFlash(%#x) = %q; want %q", val, got, want)
		}
	}
}

func TestDecodeMeteringMode(t *testing.T) {
	if got := decodeMeteringMode(5); got != "Multi-segment" {
		t.Errorf("decodeMeteringMode(5) = %q; want Multi-segment", got)
	}
	if got := decodeMeteringMode(999); got != "" {
		t.Errorf("decodeMeteringMode(999) = %q; want empty", got)
	}
}

func TestDecodeExposureProgram(t *testing.T) {
	if got := decodeExposureProgram(3); got != "Aperture priority" {
		t.Errorf("decodeExposureProgram(3) = %q; want Aperture priority", got)
	}
}

func TestDecodeColorSpace(t *testing.T) {
	cases := map[int]string{1: "sRGB", 2: "Adobe RGB", 65535: "Uncalibrated", 42: ""}
	for val, want := range cases {
		if got := decodeColorSpace(val); got != want {
			t.Errorf("decodeColorSpace(%d) = %q; want %q", val, got, want)
		}
	}
}
