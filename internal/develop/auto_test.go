package develop

import (
	"math"
	"testing"
)

func flatImage(w, h int, r, g, b float32) *Image {
	img := NewImage(w, h)
	for i := 0; i < len(img.Pix); i += 3 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2] = r, g, b
	}
	return img
}

func gradientImage(w, h int, lo, hi float32) *Image {
	img := NewImage(w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := lo + (hi-lo)*float32(x)/float32(w-1)
			i := (y*w + x) * 3
			img.Pix[i], img.Pix[i+1], img.Pix[i+2] = v, v, v
		}
	}
	return img
}

func TestAutoToneBrightensDarkScene(t *testing.T) {
	tone, _ := autoTone(sortedLuminance(gradientImage(200, 50, 0.002, 0.05)))
	if tone.Exposure <= 0.5 {
		t.Errorf("dark scene exposure = %v; want clearly positive", tone.Exposure)
	}
}

func TestAutoToneDarkensBrightScene(t *testing.T) {
	tone, _ := autoTone(sortedLuminance(gradientImage(200, 50, 0.4, 0.9)))
	if tone.Exposure >= 0 {
		t.Errorf("bright scene exposure = %v; want negative", tone.Exposure)
	}
}

func TestAutoToneProtectsHighlights(t *testing.T) {
	img := gradientImage(200, 50, 0.01, 0.05)
	for x := 190; x < 200; x++ {
		for y := 0; y < 50; y++ {
			i := (y*200 + x) * 3
			img.Pix[i], img.Pix[i+1], img.Pix[i+2] = 0.9, 0.9, 0.9
		}
	}
	tone, _ := autoTone(sortedLuminance(img))
	if tone.Exposure > 1 {
		t.Errorf("exposure %v would blow out the bright 5%% of the frame", tone.Exposure)
	}
}

func TestAutoWhiteBalanceCorrectsCast(t *testing.T) {
	x, y := TempTintToXY(3200, 0)
	cast := rec2020FromXYZ.Apply(xyToXYZ(x, y))
	k := float32(0.2 / (lumR*cast[0] + lumG*cast[1] + lumB*cast[2]))
	img := flatImage(64, 64, float32(cast[0])*k, float32(cast[1])*k, float32(cast[2])*k)
	asT, asTint := XYToTempTint(xyzToXY(whiteD65))
	wb, _ := autoWhiteBalance(img, whiteD65, asT, asTint, 0.2)
	if wb.Mode != WBCustom || wb.Temp >= asT-500 {
		t.Errorf("tungsten cast not corrected: %+v (as shot %.0f K)", wb, asT)
	}
	if shift := 1e6/wb.Temp - 1e6/asT; shift > autoWBMaxMired+1 {
		t.Errorf("shift %.1f mired exceeds the cap", shift)
	}
}

func TestAutoWhiteBalanceKeepsNeutral(t *testing.T) {
	img := flatImage(64, 64, 0.2, 0.2, 0.2)
	asT, asTint := XYToTempTint(xyzToXY(whiteD65))
	if wb, _ := autoWhiteBalance(img, whiteD65, asT, asTint, 0.2); wb.Mode != WBAsShot {
		t.Errorf("neutral image must keep as-shot WB, got %+v", wb)
	}
}

func TestAutoWhiteBalanceTrustsCameraInTwilight(t *testing.T) {
	x, y := TempTintToXY(12000, 0)
	cast := rec2020FromXYZ.Apply(xyToXYZ(x, y))
	k := float32(0.03 / (lumR*cast[0] + lumG*cast[1] + lumB*cast[2]))
	img := flatImage(64, 64, float32(cast[0])*k, float32(cast[1])*k, float32(cast[2])*k)
	asT, asTint := XYToTempTint(xyzToXY(whiteD65))
	wb, _ := autoWhiteBalance(img, whiteD65, asT, asTint, 0.012)
	if wb.Mode == WBCustom && math.Abs(1e6/wb.Temp-1e6/asT) > autoWBMaxMired*autoWBMinConf+autoWBMired {
		t.Errorf("twilight WB moved too far: %+v (as shot %.0f K)", wb, asT)
	}
}

func TestAutoToneLimitsLowKey(t *testing.T) {
	tone, notes := autoTone(sortedLuminance(gradientImage(200, 50, 0.001, 0.03)))
	if tone.Exposure > lowKeyLimit(0.01)+autoLowKeyRange+0.05 {
		t.Errorf("night scene pushed to %v EV", tone.Exposure)
	}
	if tone.Exposure <= 0 {
		t.Errorf("night scene must still be lifted a bit, got %v", tone.Exposure)
	}
	if len(notes) == 0 {
		t.Error("no notes")
	}
}

func TestAutoDetailByISO(t *testing.T) {
	low, _ := autoDetail("raw", 100)
	high, _ := autoDetail("raw", 6400)
	if low.Noise.Luma != 0 || high.Noise.Luma <= low.Noise.Luma || high.Noise.Chroma <= low.Noise.Chroma {
		t.Errorf("noise low=%+v high=%+v", low.Noise, high.Noise)
	}
	if high.Sharpen.Threshold <= low.Sharpen.Threshold {
		t.Error("sharpening masking must grow with ISO")
	}
	jpeg, _ := autoDetail("raster", 200)
	if jpeg.Noise.Luma != 0 || jpeg.Sharpen.Amount >= low.Sharpen.Amount {
		t.Errorf("jpeg detail = %+v", jpeg)
	}
}

func TestAutoLocalVibranceForDullImage(t *testing.T) {
	img := flatImage(64, 64, 0.2, 0.2, 0.21)
	local, vib, _ := autoLocal(img, sortedLuminance(img), 0)
	if vib <= 0 {
		t.Errorf("dull image vibrance = %v", vib)
	}
	if local.Clarity <= 0 {
		t.Errorf("flat image clarity = %v", local.Clarity)
	}
	if math.IsNaN(local.Dehaze) {
		t.Error("dehaze NaN")
	}
}

func TestBuiltinPresets(t *testing.T) {
	ps := BuiltinPresets()
	if len(ps) < 20 {
		t.Fatalf("only %d built-in presets", len(ps))
	}
	seen := map[string]bool{}
	for _, p := range ps {
		if seen[p.Name] {
			t.Errorf("duplicate preset %q", p.Name)
		}
		seen[p.Name] = true
		if p.ID >= 0 || !p.Builtin || len(p.Modules) == 0 {
			t.Errorf("preset %q: %+v", p.Name, p)
		}
		if err := ValidModules(p.Modules); err != nil {
			t.Errorf("preset %q modules: %v", p.Name, err)
		}
		for _, m := range p.Modules {
			if m == "exposure" && p.Recipe.Tone.Exposure == 0 {
				t.Errorf("preset %q claims exposure without setting it", p.Name)
			}
		}
	}
}

func TestPresetModules(t *testing.T) {
	got := PresetModules(Recipe{Tone: Tone{Contrast: 10}, Color: Color{BW: BW{Enabled: true}}})
	if len(got) != 2 || got[0] != "tone" || got[1] != "bw" {
		t.Errorf("modules = %v", got)
	}
}
