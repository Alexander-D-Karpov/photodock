package develop

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

const (
	EngineVersion          = "3"
	CurrentDefaultsVersion = 1

	WBAsShot = "as_shot"
	WBCustom = "custom"
)

var ErrInvalidRecipe = errors.New("invalid recipe")

type Recipe struct {
	Raw      RawParams `json:"raw"`
	WB       WBParams  `json:"wb"`
	Geometry Geometry  `json:"geometry"`
	Tone     Tone      `json:"tone"`
	Color    Color     `json:"color"`
	Local    Local     `json:"local"`
	Detail   Detail    `json:"detail"`
	Lens     Lens      `json:"lens"`
	Masks    []Mask    `json:"masks,omitempty"`
}

type RawParams struct {
	Demosaic   string `json:"demosaic,omitempty"`
	Highlights string `json:"highlights,omitempty"`
}

type WBParams struct {
	Mode string  `json:"mode,omitempty"`
	Temp float64 `json:"temp,omitempty"`
	Tint float64 `json:"tint,omitempty"`
}

type Geometry struct {
	Rotate int     `json:"rotate,omitempty"`
	FlipH  bool    `json:"flip_h,omitempty"`
	FlipV  bool    `json:"flip_v,omitempty"`
	Angle  float64 `json:"angle,omitempty"`
	Crop   *Rect   `json:"crop,omitempty"`
}

type Rect struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

type Tone struct {
	Exposure   float64 `json:"exposure,omitempty"`
	Contrast   float64 `json:"contrast,omitempty"`
	Highlights float64 `json:"highlights,omitempty"`
	Shadows    float64 `json:"shadows,omitempty"`
	Whites     float64 `json:"whites,omitempty"`
	Blacks     float64 `json:"blacks,omitempty"`
	Curve      []Point `json:"curve,omitempty"`
}

type Color struct {
	Vibrance   float64 `json:"vibrance,omitempty"`
	Saturation float64 `json:"saturation,omitempty"`
	HSL        HSL     `json:"hsl"`
	Grading    Grading `json:"grading"`
	BW         BW      `json:"bw"`
	Mixer      Mixer   `json:"mixer"`
}

type Point [2]float64

var demosaicCodes = map[string]int{
	"linear": 0, "vng": 1, "ppg": 2, "ahd": 3, "dcb": 4, "dht": 11, "aahd": 12,
}

var highlightCodes = map[string]int{
	"clip": 0, "unclip": 1, "blend": 2, "rebuild": 5,
}

var defaultsByVersion = map[int]Recipe{
	1: {
		Raw:    RawParams{Demosaic: "aahd", Highlights: "clip"},
		WB:     WBParams{Mode: WBAsShot},
		Detail: Detail{Sharpen: Sharpen{Radius: 1}},
	},
}

func Defaults(version int) (Recipe, error) {
	d, ok := defaultsByVersion[version]
	if !ok {
		return Recipe{}, fmt.Errorf("%w: unknown defaults version %d", ErrInvalidRecipe, version)
	}
	return d, nil
}

func ParseRecipe(data []byte) (Recipe, error) {
	var r Recipe
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return r, nil
	}
	if err := json.Unmarshal(trimmed, &r); err != nil {
		return Recipe{}, fmt.Errorf("%w: %w", ErrInvalidRecipe, err)
	}
	return r.Normalize()
}

func (r Recipe) Normalize() (Recipe, error) {
	var err error
	if r.Raw.Demosaic, err = normalizeChoice(r.Raw.Demosaic, demosaicCodes, "demosaic"); err != nil {
		return Recipe{}, err
	}
	if r.Raw.Highlights, err = normalizeChoice(r.Raw.Highlights, highlightCodes, "highlights"); err != nil {
		return Recipe{}, err
	}

	r.WB.Mode = strings.ToLower(strings.TrimSpace(r.WB.Mode))
	switch r.WB.Mode {
	case "", WBAsShot:
		r.WB = WBParams{Mode: r.WB.Mode}
	case WBCustom:
		if r.WB.Temp <= 0 {
			return Recipe{}, fmt.Errorf("%w: custom white balance needs temp", ErrInvalidRecipe)
		}
		r.WB.Temp = math.Round(clamp(r.WB.Temp, MinTemp, MaxTemp))
		r.WB.Tint = roundTo(clamp(r.WB.Tint, -MaxTint, MaxTint), 0.1)
	default:
		return Recipe{}, fmt.Errorf("%w: wb mode %q", ErrInvalidRecipe, r.WB.Mode)
	}

	r.Geometry.Rotate = NormalizeRotation(r.Geometry.Rotate)
	r.Geometry.Angle = roundTo(clamp(r.Geometry.Angle, -45, 45), 0.01)
	r.Geometry.Crop = normalizeCrop(r.Geometry.Crop)
	r.Tone.Exposure = roundTo(clamp(r.Tone.Exposure, -5, 5), 0.01)
	for _, v := range []*float64{
		&r.Tone.Contrast, &r.Tone.Highlights, &r.Tone.Shadows, &r.Tone.Whites, &r.Tone.Blacks,
		&r.Local.Clarity, &r.Local.Contrast, &r.Local.Dehaze,
	} {
		*v = math.Round(clamp(*v, -100, 100))
	}
	r.Tone.Curve = normalizeCurve(r.Tone.Curve)
	normalizeColor(&r.Color)

	s := &r.Detail.Sharpen
	s.Amount = math.Round(clamp(s.Amount, 0, 150))
	if s.Amount == 0 {
		*s = Sharpen{}
	} else {
		if s.Radius != 0 {
			s.Radius = roundTo(clamp(s.Radius, 0.5, 3), 0.1)
		}
		s.Threshold = math.Round(clamp(s.Threshold, 0, 100))
	}
	r.Detail.Noise.Luma = math.Round(clamp(r.Detail.Noise.Luma, 0, 100))
	r.Detail.Noise.Chroma = math.Round(clamp(r.Detail.Noise.Chroma, 0, 100))

	if !r.Lens.Enabled {
		r.Lens = Lens{}
	} else {
		r.Lens.Profile = strings.TrimSpace(r.Lens.Profile)
		if len(r.Lens.Profile) > 200 {
			r.Lens.Profile = r.Lens.Profile[:200]
		}
		r.Lens.Distortion = math.Round(clamp(r.Lens.Distortion, -100, 100))
		r.Lens.Vignetting = math.Round(clamp(r.Lens.Vignetting, -100, 100))
	}

	if r.Masks, err = normalizeMasks(r.Masks); err != nil {
		return Recipe{}, err
	}
	return r, nil
}

func normalizeCurve(pts []Point) []Point {
	if len(pts) < 2 {
		return nil
	}
	in := make([]Point, 0, len(pts))
	for _, p := range pts {
		if math.IsNaN(p[0]) || math.IsNaN(p[1]) {
			continue
		}
		in = append(in, Point{roundTo(clamp(p[0], 0, 1), 1e-4), roundTo(clamp(p[1], 0, 1), 1e-4)})
	}
	sort.SliceStable(in, func(i, j int) bool { return in[i][0] < in[j][0] })
	out := make([]Point, 0, len(in))
	for _, p := range in {
		if n := len(out); n > 0 && p[0]-out[n-1][0] < 0.01 {
			out[n-1] = p
			continue
		}
		out = append(out, p)
	}
	if len(out) > 16 {
		out = out[:16]
	}
	if len(out) < 2 {
		return nil
	}
	for _, p := range out {
		if math.Abs(p[0]-p[1]) > 1e-4 {
			return out
		}
	}
	return nil
}

func normalizeChoice(v string, allowed map[string]int, field string) (string, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return "", nil
	}
	if _, ok := allowed[v]; !ok {
		return "", fmt.Errorf("%w: %s %q", ErrInvalidRecipe, field, v)
	}
	return v, nil
}

func Resolve(r Recipe, version int) (Recipe, error) {
	d, err := Defaults(version)
	if err != nil {
		return Recipe{}, err
	}
	if r.Raw.Demosaic == "" {
		r.Raw.Demosaic = d.Raw.Demosaic
	}
	if r.Raw.Highlights == "" {
		r.Raw.Highlights = d.Raw.Highlights
	}
	if r.WB.Mode == "" {
		r.WB.Mode = d.WB.Mode
	}
	if r.Detail.Sharpen.Amount > 0 && r.Detail.Sharpen.Radius == 0 {
		r.Detail.Sharpen.Radius = d.Detail.Sharpen.Radius
	}
	return r, nil
}

// RenderHash identifies a rendering: engine version, source, external deps (LibRaw, lensdb) and recipe.
func RenderHash(sourceFingerprint string, resolved Recipe, deps ...string) (string, error) {
	c, err := canonicalJSON(resolved)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s\x00%s\x00", EngineVersion, sourceFingerprint)
	for _, d := range deps {
		_, _ = fmt.Fprintf(h, "%s\x00", d)
	}
	h.Write(c)
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

func canonicalJSON(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var generic any
	if err := json.Unmarshal(b, &generic); err != nil {
		return nil, err
	}
	pruned := prune(generic)
	if pruned == nil {
		pruned = map[string]any{}
	}
	return json.Marshal(pruned)
}

func prune(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			if p := prune(x); p != nil {
				out[k] = p
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case []any:
		if len(t) == 0 {
			return nil
		}
		out := make([]any, len(t))
		for i, x := range t {
			if m, ok := x.(map[string]any); ok {
				p := prune(m)
				if p == nil {
					p = map[string]any{}
				}
				out[i] = p
				continue
			}
			out[i] = x
		}
		return out
	case bool:
		if !t {
			return nil
		}
	case float64:
		if t == 0 {
			return nil
		}
	case string:
		if t == "" {
			return nil
		}
	case nil:
		return nil
	}
	return v
}

func NormalizeRotation(rotation int) int {
	r := rotation % 360
	if r < 0 {
		r += 360
	}
	return (r / 90) * 90
}

func normalizeCrop(c *Rect) *Rect {
	if c == nil {
		return nil
	}
	x := roundTo(clamp(c.X, 0, 0.99), 1e-5)
	y := roundTo(clamp(c.Y, 0, 0.99), 1e-5)
	w := roundTo(clamp(c.W, 0.01, 1-x), 1e-5)
	h := roundTo(clamp(c.H, 0.01, 1-y), 1e-5)
	if x == 0 && y == 0 && w >= 1 && h >= 1 {
		return nil
	}
	return &Rect{X: x, Y: y, W: w, H: h}
}

func clamp(v, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, v))
}

func roundTo(v, step float64) float64 {
	return math.Round(v/step) * step
}

type Local struct {
	Clarity  float64 `json:"clarity,omitempty"`
	Contrast float64 `json:"contrast,omitempty"`
	Dehaze   float64 `json:"dehaze,omitempty"`
}

type Detail struct {
	Sharpen Sharpen `json:"sharpen"`
	Noise   Noise   `json:"noise"`
}

type Sharpen struct {
	Amount    float64 `json:"amount,omitempty"`
	Radius    float64 `json:"radius,omitempty"`
	Threshold float64 `json:"threshold,omitempty"`
}

type Noise struct {
	Luma   float64 `json:"luma,omitempty"`
	Chroma float64 `json:"chroma,omitempty"`
}

type Lens struct {
	Enabled    bool    `json:"enabled,omitempty"`
	Profile    string  `json:"profile,omitempty"`
	Distortion float64 `json:"distortion,omitempty"`
	Vignetting float64 `json:"vignetting,omitempty"`
	NoTCA      bool    `json:"no_tca,omitempty"`
}

type HSLBand struct {
	Hue float64 `json:"h,omitempty"`
	Sat float64 `json:"s,omitempty"`
	Lum float64 `json:"l,omitempty"`
}

type HSL struct {
	Red     HSLBand `json:"red"`
	Orange  HSLBand `json:"orange"`
	Yellow  HSLBand `json:"yellow"`
	Green   HSLBand `json:"green"`
	Aqua    HSLBand `json:"aqua"`
	Blue    HSLBand `json:"blue"`
	Purple  HSLBand `json:"purple"`
	Magenta HSLBand `json:"magenta"`
}

func (h *HSL) bands() [8]*HSLBand {
	return [8]*HSLBand{&h.Red, &h.Orange, &h.Yellow, &h.Green, &h.Aqua, &h.Blue, &h.Purple, &h.Magenta}
}

type HueWeights struct {
	Red     float64 `json:"red,omitempty"`
	Orange  float64 `json:"orange,omitempty"`
	Yellow  float64 `json:"yellow,omitempty"`
	Green   float64 `json:"green,omitempty"`
	Aqua    float64 `json:"aqua,omitempty"`
	Blue    float64 `json:"blue,omitempty"`
	Purple  float64 `json:"purple,omitempty"`
	Magenta float64 `json:"magenta,omitempty"`
}

func (w *HueWeights) values() [8]*float64 {
	return [8]*float64{&w.Red, &w.Orange, &w.Yellow, &w.Green, &w.Aqua, &w.Blue, &w.Purple, &w.Magenta}
}

type Wheel struct {
	Hue float64 `json:"hue,omitempty"`
	Sat float64 `json:"sat,omitempty"`
	Lum float64 `json:"lum,omitempty"`
}

// Grading is split-toning by tonal range; Blending is stored as an offset from 50 so zero stays neutral.
type Grading struct {
	Shadows    Wheel   `json:"shadows"`
	Midtones   Wheel   `json:"midtones"`
	Highlights Wheel   `json:"highlights"`
	Global     Wheel   `json:"global"`
	Blending   float64 `json:"blending,omitempty"`
	Balance    float64 `json:"balance,omitempty"`
}

type BW struct {
	Enabled bool       `json:"enabled,omitempty"`
	Mix     HueWeights `json:"mix"`
}

type MixerRow struct {
	R float64 `json:"r,omitempty"`
	G float64 `json:"g,omitempty"`
	B float64 `json:"b,omitempty"`
}

type Mixer struct {
	Red   MixerRow `json:"red"`
	Green MixerRow `json:"green"`
	Blue  MixerRow `json:"blue"`
}

func normalizeColor(c *Color) {
	c.Vibrance = math.Round(clamp(c.Vibrance, -100, 100))
	c.Saturation = math.Round(clamp(c.Saturation, -100, 100))
	for _, b := range c.HSL.bands() {
		b.Hue = math.Round(clamp(b.Hue, -100, 100))
		b.Sat = math.Round(clamp(b.Sat, -100, 100))
		b.Lum = math.Round(clamp(b.Lum, -100, 100))
	}
	g := &c.Grading
	for _, w := range []*Wheel{&g.Shadows, &g.Midtones, &g.Highlights, &g.Global} {
		w.Sat = math.Round(clamp(w.Sat, 0, 100))
		w.Lum = math.Round(clamp(w.Lum, -100, 100))
		if w.Sat == 0 {
			w.Hue = 0
		} else {
			w.Hue = math.Mod(math.Round(w.Hue), 360)
			if w.Hue < 0 {
				w.Hue += 360
			}
		}
	}
	g.Blending = math.Round(clamp(g.Blending, -50, 50))
	g.Balance = math.Round(clamp(g.Balance, -100, 100))
	if !c.BW.Enabled {
		c.BW = BW{}
	} else {
		for _, v := range c.BW.Mix.values() {
			*v = math.Round(clamp(*v, -100, 100))
		}
	}
	for _, row := range []*MixerRow{&c.Mixer.Red, &c.Mixer.Green, &c.Mixer.Blue} {
		row.R = math.Round(clamp(row.R, -200, 200))
		row.G = math.Round(clamp(row.G, -200, 200))
		row.B = math.Round(clamp(row.B, -200, 200))
	}
}
