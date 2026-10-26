package develop

import "sync"

// Built-in presets: negative IDs, undeletable; modules derived from what each recipe sets.

var (
	builtinOnce  sync.Once
	builtinCache []Preset
)

type builtinDef struct {
	name   string
	group  string
	recipe Recipe
}

func vignetteMask(ev float64) Mask {
	return Mask{
		ID: "vignette", Type: "radial", CX: 0.5, CY: 0.5, RX: 0.62, RY: 0.46,
		Feather: 85, Invert: true, Adjust: MaskAdjust{Exposure: ev},
	}
}

func builtinDefs() []builtinDef {
	return []builtinDef{
		{"Natural", "Color", Recipe{
			Tone:   Tone{Contrast: 8},
			Local:  Local{Clarity: 5},
			Color:  Color{Vibrance: 12},
			Detail: Detail{Sharpen: Sharpen{Amount: 30, Radius: 1, Threshold: 15}},
		}},
		{"Vivid", "Color", Recipe{
			Tone:  Tone{Contrast: 20, Highlights: -10, Shadows: 10},
			Local: Local{Clarity: 12, Dehaze: 8},
			Color: Color{Vibrance: 30, Saturation: 8},
		}},
		{"Muted", "Color", Recipe{
			Tone:  Tone{Contrast: -8, Highlights: -10, Shadows: 10},
			Color: Color{Vibrance: -15, Saturation: -20},
		}},
		{"Golden hour", "Color", Recipe{
			Tone: Tone{Contrast: 10, Highlights: -15, Shadows: 10},
			Color: Color{
				Vibrance: 10,
				HSL:      HSL{Orange: HSLBand{Sat: 15}, Yellow: HSLBand{Sat: 15, Hue: -10}},
				Grading:  Grading{Global: Wheel{Hue: 40, Sat: 12}, Highlights: Wheel{Hue: 45, Sat: 15}},
			},
		}},
		{"Cool morning", "Color", Recipe{
			Tone: Tone{Contrast: -5, Shadows: 10},
			Color: Color{
				HSL:     HSL{Blue: HSLBand{Sat: 15}, Aqua: HSLBand{Sat: 10}},
				Grading: Grading{Global: Wheel{Hue: 210, Sat: 10}, Shadows: Wheel{Hue: 220, Sat: 15}},
			},
		}},
		{"Teal & orange", "Creative", Recipe{
			Tone: Tone{Contrast: 12, Highlights: -15},
			Color: Color{
				Vibrance: 10,
				HSL: HSL{
					Orange: HSLBand{Hue: -5, Sat: 10},
					Aqua:   HSLBand{Hue: -10, Sat: 10},
					Blue:   HSLBand{Hue: -10},
					Green:  HSLBand{Hue: 20, Sat: -25},
				},
				Grading: Grading{Shadows: Wheel{Hue: 195, Sat: 25}, Highlights: Wheel{Hue: 35, Sat: 20}, Balance: -10},
			},
		}},
		{"Moody", "Creative", Recipe{
			Tone:  Tone{Exposure: -0.3, Contrast: 15, Highlights: -30, Shadows: -10, Blacks: -15},
			Local: Local{Dehaze: 10},
			Color: Color{
				Vibrance: -10, Saturation: -15,
				Grading: Grading{Shadows: Wheel{Hue: 200, Sat: 15}, Highlights: Wheel{Hue: 40, Sat: 10}},
			},
			Masks: []Mask{vignetteMask(-0.6)},
		}},
		{"Soft & airy", "Creative", Recipe{
			Tone:  Tone{Exposure: 0.3, Contrast: -15, Highlights: -20, Shadows: 25, Blacks: 10},
			Local: Local{Clarity: -10},
			Color: Color{Vibrance: 5, Saturation: -10, Grading: Grading{Highlights: Wheel{Hue: 45, Sat: 8}}},
		}},
		{"Cinematic", "Creative", Recipe{
			Tone: Tone{Contrast: 12, Highlights: -25, Shadows: 10},
			Color: Color{
				Saturation: -10,
				Grading:    Grading{Shadows: Wheel{Hue: 190, Sat: 30}, Highlights: Wheel{Hue: 30, Sat: 20}, Blending: -10},
			},
			Masks: []Mask{vignetteMask(-0.4)},
		}},
		{"Film warm", "Creative", Recipe{
			Tone: Tone{Contrast: -5, Curve: []Point{{0, 0.06}, {0.25, 0.24}, {0.75, 0.78}, {1, 0.97}}},
			Color: Color{
				Saturation: -5,
				HSL: HSL{
					Orange: HSLBand{Sat: -5, Lum: 5},
					Green:  HSLBand{Hue: 15, Sat: -20},
					Blue:   HSLBand{Sat: -10},
				},
				Grading: Grading{Shadows: Wheel{Hue: 190, Sat: 10}, Highlights: Wheel{Hue: 40, Sat: 12}},
			},
		}},
		{"Faded matte", "Creative", Recipe{
			Tone:  Tone{Contrast: -10, Curve: []Point{{0, 0.1}, {0.5, 0.5}, {1, 0.95}}},
			Color: Color{Saturation: -15},
		}},
		{"B&W classic", "B&W", Recipe{
			Tone:  Tone{Contrast: 20},
			Local: Local{Clarity: 10},
			Color: Color{BW: BW{Enabled: true, Mix: HueWeights{Red: 10, Orange: 15, Yellow: 10, Blue: -15}}},
		}},
		{"B&W high contrast", "B&W", Recipe{
			Tone:  Tone{Contrast: 45, Whites: 15, Blacks: -20},
			Local: Local{Clarity: 20},
			Color: Color{BW: BW{Enabled: true, Mix: HueWeights{Red: 20, Orange: 10, Yellow: 20, Green: -10, Aqua: -20, Blue: -40}}},
		}},
		{"B&W soft", "B&W", Recipe{
			Tone:  Tone{Contrast: -5, Highlights: -15, Shadows: 15},
			Local: Local{Clarity: -5},
			Color: Color{BW: BW{Enabled: true, Mix: HueWeights{Orange: 15, Red: 5}}},
		}},
		{"B&W selenium", "B&W", Recipe{
			Tone: Tone{Contrast: 15},
			Color: Color{
				BW:      BW{Enabled: true, Mix: HueWeights{Orange: 10, Blue: -20}},
				Grading: Grading{Shadows: Wheel{Hue: 260, Sat: 12}, Highlights: Wheel{Hue: 40, Sat: 6}},
			},
		}},
		{"Portrait", "Subject", Recipe{
			Tone:   Tone{Contrast: 5, Highlights: -10, Shadows: 10},
			Local:  Local{Clarity: -10},
			Color:  Color{Vibrance: 8, HSL: HSL{Orange: HSLBand{Sat: -8, Lum: 10}, Red: HSLBand{Sat: -5}}},
			Detail: Detail{Sharpen: Sharpen{Amount: 25, Radius: 1.2, Threshold: 30}, Noise: Noise{Luma: 10}},
		}},
		{"Landscape", "Subject", Recipe{
			Tone:  Tone{Contrast: 15, Highlights: -20, Shadows: 15},
			Local: Local{Clarity: 20, Dehaze: 15},
			Color: Color{
				Vibrance: 20,
				HSL:      HSL{Blue: HSLBand{Sat: 15, Lum: -10}, Green: HSLBand{Sat: 10}, Aqua: HSLBand{Sat: 10}},
			},
		}},
		{"Night city", "Subject", Recipe{
			Tone:   Tone{Highlights: -35, Shadows: 20},
			Local:  Local{Clarity: 10},
			Color:  Color{Vibrance: 15, Grading: Grading{Shadows: Wheel{Hue: 230, Sat: 10}}},
			Detail: Detail{Noise: Noise{Luma: 25, Chroma: 40}},
		}},
		{"Food", "Subject", Recipe{
			Tone:  Tone{Exposure: 0.15, Contrast: 10, Shadows: 15},
			Local: Local{Clarity: 8},
			Color: Color{Vibrance: 15, HSL: HSL{Red: HSLBand{Sat: 10}, Orange: HSLBand{Sat: 10}, Yellow: HSLBand{Sat: 10}}},
		}},
		{"Vignette", "Utility", Recipe{Masks: []Mask{vignetteMask(-0.5)}}},
		{"Extra detail", "Utility", Recipe{
			Local:  Local{Clarity: 10},
			Detail: Detail{Sharpen: Sharpen{Amount: 60, Radius: 0.8, Threshold: 20}},
		}},
		{"High-ISO cleanup", "Utility", Recipe{
			Detail: Detail{Noise: Noise{Luma: 40, Chroma: 50}, Sharpen: Sharpen{Amount: 30, Radius: 1, Threshold: 30}},
		}},
	}
}

func BuiltinPresets() []Preset {
	builtinOnce.Do(func() {
		for i, d := range builtinDefs() {
			r, err := d.recipe.Normalize()
			if err != nil {
				continue
			}
			builtinCache = append(builtinCache, Preset{
				ID:      -(i + 1),
				Name:    d.name,
				Group:   d.group,
				Builtin: true,
				Recipe:  r,
				Modules: PresetModules(r),
			})
		}
	})
	return builtinCache
}
