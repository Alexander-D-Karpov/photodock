package develop

import (
	"strings"
	"testing"
)

func TestSummarizeNeutralIsNil(t *testing.T) {
	if Summarize(Recipe{WB: WBParams{Mode: WBAsShot}}) != nil {
		t.Error("a neutral recipe must have no summary")
	}
}

func TestSummarizeListsModules(t *testing.T) {
	s := Summarize(Recipe{
		WB:       WBParams{Mode: WBCustom, Temp: 6900, Tint: 6},
		Tone:     Tone{Exposure: 0.45, Contrast: 12, Highlights: -30},
		Color:    Color{BW: BW{Enabled: true}, HSL: HSL{Blue: HSLBand{Sat: -40}}},
		Lens:     Lens{Enabled: true, Profile: "Sony|E PZ 16-50mm"},
		Geometry: Geometry{Crop: &Rect{W: 0.5, H: 0.5}},
		Masks:    []Mask{{Type: "radial", Adjust: MaskAdjust{Exposure: 1}}, {Type: "linear", Off: true, Adjust: MaskAdjust{Exposure: 1}}},
	})
	if s == nil {
		t.Fatal("expected a summary")
	}
	got := map[string]string{}
	for _, r := range s.Rows {
		got[r.Label] = r.Value
	}
	want := map[string]string{
		"White balance":   "6900 K, tint +6",
		"Exposure":        "+0.45 EV",
		"Tone":            "Contrast +12 · Highlights −30",
		"HSL":             "Blue",
		"Black & white":   "Converted",
		"Lens correction": "Sony E PZ 16-50mm",
		"Geometry":        "cropped",
		"Masks":           "1 local adjustment",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q; want %q", k, got[k], v)
		}
	}
	chips := strings.Join(s.Chips, "|")
	for _, c := range []string{"6900 K", "+0.45 EV", "B&W", "Lens corrected", "Cropped"} {
		if !strings.Contains(chips, c) {
			t.Errorf("chip %q missing from %v", c, s.Chips)
		}
	}
}
