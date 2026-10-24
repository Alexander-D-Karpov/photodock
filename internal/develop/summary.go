package develop

import (
	"context"
	"fmt"
	"math"
	"strings"
)

// Summary describes a recipe for the public photo page: a few headline chips
// and one row per module that changes the image.
type Summary struct {
	Chips []string
	Rows  []SummaryRow
}

type SummaryRow struct {
	Label string
	Value string
}

var bandNames = [8]string{"Red", "Orange", "Yellow", "Green", "Aqua", "Blue", "Purple", "Magenta"}

func signedInt(v float64) string {
	if v > 0 {
		return fmt.Sprintf("+%.0f", v)
	}
	return fmt.Sprintf("−%.0f", math.Abs(v))
}

func signedEV(v float64) string {
	if v > 0 {
		return fmt.Sprintf("+%.2f EV", v)
	}
	return fmt.Sprintf("−%.2f EV", math.Abs(v))
}

// joinNonZero renders "Label ±v" for every non-zero value, joined by " · ".
func joinNonZero(pairs ...any) string {
	var out []string
	for i := 0; i+1 < len(pairs); i += 2 {
		if v := pairs[i+1].(float64); v != 0 {
			out = append(out, pairs[i].(string)+" "+signedInt(v))
		}
	}
	return strings.Join(out, " · ")
}

func lensProfileName(id string) string {
	return strings.Replace(id, "|", " ", 1)
}

// Summarize lists what a recipe does, skipping neutral modules.
func Summarize(r Recipe) *Summary {
	s := &Summary{}
	add := func(label, value string) {
		if value != "" {
			s.Rows = append(s.Rows, SummaryRow{Label: label, Value: value})
		}
	}

	if r.WB.Mode == WBCustom {
		wb := fmt.Sprintf("%.0f K", r.WB.Temp)
		if r.WB.Tint != 0 {
			wb += ", tint " + signedInt(r.WB.Tint)
		}
		add("White balance", wb)
		s.Chips = append(s.Chips, fmt.Sprintf("%.0f K", r.WB.Temp))
	}
	if r.Tone.Exposure != 0 {
		add("Exposure", signedEV(r.Tone.Exposure))
		s.Chips = append(s.Chips, signedEV(r.Tone.Exposure))
	}
	t := r.Tone
	add("Tone", joinNonZero("Contrast", t.Contrast, "Highlights", t.Highlights, "Shadows", t.Shadows, "Whites", t.Whites, "Blacks", t.Blacks))
	if len(t.Curve) > 0 {
		add("Tone curve", fmt.Sprintf("%d points", len(t.Curve)))
	}
	l := r.Local
	add("Presence", joinNonZero("Clarity", l.Clarity, "Local contrast", l.Contrast, "Dehaze", l.Dehaze))

	c := r.Color
	add("Color", joinNonZero("Vibrance", c.Vibrance, "Saturation", c.Saturation))
	var bands []string
	for i, b := range c.HSL.bands() {
		if *b != (HSLBand{}) {
			bands = append(bands, bandNames[i])
		}
	}
	if len(bands) > 0 {
		add("HSL", strings.Join(bands, ", "))
	}
	g := c.Grading
	var wheels []string
	for _, w := range []struct {
		name string
		w    Wheel
	}{{"shadows", g.Shadows}, {"midtones", g.Midtones}, {"highlights", g.Highlights}, {"global", g.Global}} {
		if w.w.Sat != 0 || w.w.Lum != 0 {
			wheels = append(wheels, w.name)
		}
	}
	if len(wheels) > 0 {
		add("Color grading", strings.Join(wheels, ", "))
		s.Chips = append(s.Chips, "Graded")
	}
	if c.BW.Enabled {
		add("Black & white", "Converted")
		s.Chips = append(s.Chips, "B&W")
	}
	if c.Mixer != (Mixer{}) {
		add("Channel mixer", "Adjusted")
	}

	d := r.Detail
	if d.Sharpen.Amount > 0 {
		add("Sharpening", fmt.Sprintf("%.0f", d.Sharpen.Amount))
	}
	if d.Noise.Luma > 0 || d.Noise.Chroma > 0 {
		add("Noise reduction", fmt.Sprintf("luminance %.0f · color %.0f", d.Noise.Luma, d.Noise.Chroma))
	}

	if r.Lens.Enabled {
		name := "Lensfun profile"
		if r.Lens.Profile != "" {
			name = lensProfileName(r.Lens.Profile)
		}
		add("Lens correction", name)
		s.Chips = append(s.Chips, "Lens corrected")
	}

	geo := r.Geometry
	var gparts []string
	if geo.Crop != nil {
		gparts = append(gparts, "cropped")
		s.Chips = append(s.Chips, "Cropped")
	}
	if geo.Angle != 0 {
		gparts = append(gparts, fmt.Sprintf("straightened %+.1f°", geo.Angle))
	}
	if geo.FlipH || geo.FlipV {
		gparts = append(gparts, "mirrored")
	}
	if len(gparts) > 0 {
		add("Geometry", strings.Join(gparts, ", "))
	}

	active := 0
	for _, m := range r.Masks {
		if !m.Off && m.Adjust != (MaskAdjust{}) {
			active++
		}
	}
	if active > 0 {
		label := "1 local adjustment"
		if active > 1 {
			label = fmt.Sprintf("%d local adjustments", active)
		}
		add("Masks", label)
		s.Chips = append(s.Chips, label)
	}
	if len(s.Rows) == 0 {
		return nil
	}
	return s
}

// PhotoSummary summarises the photo's current recipe, or nil when unedited.
func (e *Engine) PhotoSummary(ctx context.Context, id int) *Summary {
	row, err := e.loadEdit(ctx, id)
	if err != nil || row == nil {
		return nil
	}
	return Summarize(row.Recipe)
}
