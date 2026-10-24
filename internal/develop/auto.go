package develop

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"time"
)

// Auto analyses the frame (after crop and lens correction) and proposes
// per-module settings. Tuning knobs are the constants below.

const (
	autoSampleSide   = 768
	autoKey          = 0.17 // target scene key (log-average luminance)
	autoHighTarget   = 0.92
	autoHighSlack    = 0.7 // EV allowed past the highlights, relying on the Highlights slider
	autoMaxEV        = 3.0
	autoWBBlend      = 0.5 // share of the estimate vs. the camera white balance
	autoWBMired      = 8.0
	autoWBTint       = 4.0
	autoWBMaxChroma  = 0.2
	autoMinNeutral   = 500
	autoShadesPower  = 6.0
	autoDetailRadius = 0.01
	autoLensScore    = 2.2
	autoLensGap      = 0.3
	autoBatchTimeout = 2 * time.Minute

	// Low-key (dusk/night) scenes: cap the exposure lift and trust the
	// camera white balance more as the scene darkens.
	autoLowKey      = 0.06
	autoDarkKey     = 0.01
	autoLowKeyBoost = 0.6
	autoLowKeyRange = 0.8
	autoWBMaxMired  = 30.0
	autoWBMaxTint   = 10.0
	autoWBMinConf   = 0.3
)

type AutoOptions struct {
	Only string `json:"only"`
	WB   bool   `json:"wb"`
}

type AutoResult struct {
	Recipe  Recipe   `json:"recipe"`
	Modules []string `json:"modules"`
	Notes   []string `json:"notes"`
}

func (e *Engine) Auto(ctx context.Context, id int, rec Recipe, opts AutoOptions) (*AutoResult, error) {
	resolved, src, err := e.prepare(ctx, id, rec)
	if err != nil {
		return nil, err
	}
	return e.autoSource(ctx, src, resolved, opts)
}

func (e *Engine) AutoStaged(ctx context.Context, sid string, rec Recipe, opts AutoOptions) (*AutoResult, error) {
	resolved, src, err := e.prepareStaged(ctx, sid, rec)
	if err != nil {
		return nil, err
	}
	return e.autoSource(ctx, src, resolved, opts)
}

// AutoBatch auto-develops the photos in the background, one at a time.
func (e *Engine) AutoBatch(ids []int, opts AutoOptions) error {
	if err := checkBatch(ids); err != nil {
		return err
	}
	ids = append([]int(nil), ids...)
	go func() {
		applied, failed := 0, 0
		for _, id := range ids {
			if err := e.autoOne(id, opts); err != nil {
				failed++
				log.Printf("develop: auto photo %d: %v", id, err)
				continue
			}
			applied++
		}
		e.kick()
		log.Printf("develop: batch auto done: %d applied, %d failed", applied, failed)
	}()
	return nil
}

func (e *Engine) autoOne(id int, opts AutoOptions) error {
	ctx, cancel := context.WithTimeout(context.Background(), autoBatchTimeout)
	defer cancel()
	cur, rev, err := e.currentRecipe(ctx, id)
	if err != nil {
		return err
	}
	res, err := e.Auto(ctx, id, cur, opts)
	if err != nil {
		return err
	}
	if len(res.Modules) == 0 {
		return nil
	}
	merged, err := MergeModules(cur, res.Recipe, res.Modules)
	if err != nil {
		return err
	}
	if sameRecipe(cur, merged) {
		return nil
	}
	return e.saveRecipe(ctx, id, merged, rev, "Auto", false)
}

func (e *Engine) autoSource(ctx context.Context, src *Source, resolved Recipe, opts AutoOptions) (*AutoResult, error) {
	switch opts.Only {
	case "", "tone", "wb":
	default:
		return nil, fmt.Errorf("%w: unknown auto scope %q", ErrInvalidRecipe, opts.Only)
	}
	wantWB := opts.Only == "wb" || (opts.Only == "" && opts.WB)
	wantTone := opts.Only == "" || opts.Only == "tone"
	wantRest := opts.Only == ""

	lens, err := e.lensFor(ctx, src, resolved)
	if err != nil {
		return nil, err
	}
	from := whiteD65
	if src.Kind == "raw" {
		if from, err = e.asShotWhite(ctx, src); err != nil {
			return nil, err
		}
	}
	base := resolved
	base.WB = WBParams{Mode: WBAsShot}

	release, err := e.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	l, err := e.proxy(ctx, src, base)
	if err != nil {
		return nil, err
	}
	frame, _, _, _ := e.frameFromProxy(l, resolved, lens)
	img := frame.FitWithin(autoSampleSide, autoSampleSide)

	res := &AutoResult{Modules: []string{}, Notes: []string{}}
	finish := func() (*AutoResult, error) {
		r, err := res.Recipe.Normalize()
		if err != nil {
			return nil, err
		}
		res.Recipe = r
		return res, nil
	}

	wb := resolved.WB
	if wantWB {
		asT, asTint := XYToTempTint(xyzToXY(from))
		sceneKey := logKey(sortedLuminance(img))
		var note string
		wb, note = autoWhiteBalance(img, from, asT, asTint, sceneKey)
		res.Recipe.WB = wb
		res.Modules = append(res.Modules, "wb")
		res.Notes = append(res.Notes, note)
	}
	if wb.Mode == WBCustom {
		x, y := TempTintToXY(wb.Temp, wb.Tint)
		applyMatrix(img, wbMatrix(xyToXYZ(x, y), from))
	}
	if !wantTone && !wantRest {
		return finish()
	}

	lum := sortedLuminance(img)
	if len(lum) == 0 {
		return finish()
	}
	tone, notes := autoTone(lum)
	if wantTone {
		res.Recipe.Tone = tone
		res.Modules = append(res.Modules, "exposure", "tone")
		res.Notes = append(res.Notes, notes...)
	}
	if !wantRest {
		return finish()
	}

	local, vibrance, notes := autoLocal(img, lum, tone.Exposure)
	res.Recipe.Local = local
	res.Recipe.Color.Vibrance = vibrance
	res.Modules = append(res.Modules, "local", "presence")
	res.Notes = append(res.Notes, notes...)

	info := e.dec.LensInfo(ctx, src.Abs, src.Fingerprint)
	detail, note := autoDetail(src.Kind, info.ISO)
	res.Recipe.Detail = detail
	res.Modules = append(res.Modules, "noise", "sharpen")
	res.Notes = append(res.Notes, note)

	if ln, note := e.autoLens(ctx, src, resolved, info); ln != nil {
		res.Recipe.Lens = *ln
		res.Modules = append(res.Modules, "lens")
		res.Notes = append(res.Notes, note)
	}
	return finish()
}

func signedNum(v float64) string {
	if v > 0 {
		return fmt.Sprintf("+%g", v)
	}
	return fmt.Sprintf("%g", v)
}

func applyMatrix(img *Image, m Mat3) {
	mm := toMat32(m)
	parallelRows(img.H, func(y0, y1 int) {
		for i := y0 * img.W * 3; i < y1*img.W*3; i += 3 {
			r, g, b := img.Pix[i], img.Pix[i+1], img.Pix[i+2]
			img.Pix[i] = mm[0]*r + mm[1]*g + mm[2]*b
			img.Pix[i+1] = mm[3]*r + mm[4]*g + mm[5]*b
			img.Pix[i+2] = mm[6]*r + mm[7]*g + mm[8]*b
		}
	})
}

func sortedLuminance(img *Image) []float64 {
	out := make([]float64, 0, img.W*img.H)
	for i := 0; i < len(img.Pix); i += 3 {
		y := float64(lumR*img.Pix[i] + lumG*img.Pix[i+1] + lumB*img.Pix[i+2])
		out = append(out, math.Max(y, 0))
	}
	sort.Float64s(out)
	return out
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	return sorted[clampInt(int(p*float64(len(sorted)-1)), 0, len(sorted)-1)]
}

// logKey is the geometric mean luminance, ignoring the extreme 2% tails.
func logKey(sorted []float64) float64 {
	n := len(sorted)
	lo, hi := int(0.02*float64(n)), int(0.98*float64(n))
	if hi <= lo {
		lo, hi = 0, n
	}
	sum := 0.0
	for _, v := range sorted[lo:hi] {
		sum += math.Log(math.Max(v, 1e-4))
	}
	return math.Exp(sum / float64(hi-lo))
}

// autoWhiteBalance is shades-of-gray over near-neutral mid-tones, blended
// with the camera's value and kept as-shot when the two barely differ. The
// shift is capped and trusted less in dark scenes, where twilight blue reads
// as a cast.
func autoWhiteBalance(img *Image, from [3]float64, asT, asTint, sceneKey float64) (WBParams, string) {
	keep := WBParams{Mode: WBAsShot}
	var sum [3]float64
	n := 0
	for i := 0; i < len(img.Pix); i += 3 {
		r, g, b := float64(img.Pix[i]), float64(img.Pix[i+1]), float64(img.Pix[i+2])
		if r <= 0 || g <= 0 || b <= 0 || math.Max(r, math.Max(g, b)) >= 0.95 {
			continue
		}
		y := lumR*r + lumG*g + lumB*b
		if y < 0.02 || y > 0.8 {
			continue
		}
		if _, a, bb := toOKLab(r, g, b); math.Hypot(a, bb) > autoWBMaxChroma {
			continue
		}
		sum[0] += math.Pow(r, autoShadesPower)
		sum[1] += math.Pow(g, autoShadesPower)
		sum[2] += math.Pow(b, autoShadesPower)
		n++
	}
	if n < autoMinNeutral {
		return keep, "White balance: not enough neutral areas, kept the camera value"
	}
	est := [3]float64{
		math.Pow(sum[0]/float64(n), 1/autoShadesPower),
		math.Pow(sum[1]/float64(n), 1/autoShadesPower),
		math.Pow(sum[2]/float64(n), 1/autoShadesPower),
	}
	got, err := wbFromSample(est, from)
	if err != nil {
		return keep, "White balance: kept the camera value"
	}
	confidence := clamp((sceneKey-autoDarkKey)/(autoLowKey-autoDarkKey), autoWBMinConf, 1)
	weight := autoWBBlend * confidence
	// The cap scales with confidence too: ~30 mired (≈1600 K near 6500 K) for
	// normal scenes, ~9 mired (≈400 K) for dusk and night.
	miredLimit := autoWBMaxMired * confidence
	tintLimit := autoWBMaxTint * confidence
	asMired := 1e6 / asT
	shift := clamp(weight*(1e6/got.Temp-asMired), -miredLimit, miredLimit)
	tintShift := clamp(weight*(got.Tint-asTint), -tintLimit, tintLimit)
	if math.Abs(shift) < autoWBMired && math.Abs(tintShift) < autoWBTint {
		return keep, "White balance: camera value looks right"
	}
	wb := WBParams{
		Mode: WBCustom,
		Temp: math.Round(clamp(1e6/(asMired+shift), MinTemp, MaxTemp)),
		Tint: math.Round(clamp(asTint+tintShift, -MaxTint, MaxTint)),
	}
	note := fmt.Sprintf("White balance %.0f K, tint %s", wb.Temp, signedNum(wb.Tint))
	if confidence < 1 {
		note += " (dark scene, kept close to camera)"
	}
	return wb, note
}

func autoTone(lum []float64) (Tone, []string) {
	key := logKey(lum)
	evMid := math.Log2(autoKey / math.Max(key, 1e-4))
	evHigh := math.Log2(autoHighTarget/math.Max(percentile(lum, 0.995), 1e-4)) + autoHighSlack
	ev := math.Min(evMid, evHigh)
	lowKey := key < autoLowKey
	limited := false
	if lowKey {
		if limit := lowKeyLimit(key); ev > limit {
			ev = limit
			limited = true
		}
	}
	ev = roundTo(clamp(ev, -autoMaxEV, autoMaxEV), 0.05)

	gain := math.Exp2(ev)
	enc := func(p float64) float64 { return math.Pow(percentile(lum, p)*gain, 1/2.2) }
	p002, p10, p25, p75, p99, p998 := enc(0.002), enc(0.10), enc(0.25), enc(0.75), enc(0.99), enc(0.998)

	highlights := 0.0
	if p99 > 0.88 {
		highlights = -clamp((p99-0.8)/0.4*80, 0, 70)
	}
	shadows := 0.0
	if p10 < 0.28 {
		shadows = clamp((0.28-p10)/0.28*60, 0, 50)
		if lowKey {
			shadows /= 2
		}
	}
	// Night lights already give the top end; stretching whites in a low-key
	// frame would only lift the background.
	whites := 0.0
	if p998 < 0.95 && !lowKey {
		whites = clamp((1-p998/0.97)/0.3*100, 0, 40)
	} else if p998 > 1.05 && highlights == 0 {
		whites = -15
	}
	blacks := clamp(-(p002-0.015)/0.1*100, -35, 15)
	contrast := clamp((0.30-(p75-p25))/0.15*30, -15, 30)

	t := Tone{
		Exposure:   ev,
		Contrast:   math.Round(contrast),
		Highlights: math.Round(highlights),
		Shadows:    math.Round(shadows),
		Whites:     math.Round(whites),
		Blacks:     math.Round(blacks),
	}
	notes := []string{fmt.Sprintf("Exposure %s EV", signedNum(t.Exposure))}
	if limited {
		notes[0] += " (low-key scene, brightening limited)"
	}
	notes = append(notes,
		fmt.Sprintf("Highlights %s · Shadows %s · Contrast %s", signedNum(t.Highlights), signedNum(t.Shadows), signedNum(t.Contrast)),
		fmt.Sprintf("Whites %s · Blacks %s", signedNum(t.Whites), signedNum(t.Blacks)),
	)
	return t, notes
}

func autoLocal(img *Image, lum []float64, ev float64) (Local, float64, []string) {
	w, h := img.W, img.H
	gain := math.Exp2(ev)
	g32 := float32(gain)
	s := make([]float32, w*h)
	for p := range s {
		i := p * 3
		y := lumR*img.Pix[i] + lumG*img.Pix[i+1] + lumB*img.Pix[i+2]
		s[p] = float32(math.Sqrt(math.Max(float64(y*g32), 0)))
	}
	base := make([]float32, len(s))
	copy(base, s)
	base = boxBlur3(base, w, h, max(1, int(autoDetailRadius*float64(max(w, h)))))

	detail, nd := 0.0, 0
	chroma, nc := 0.0, 0
	for p := range s {
		if v := s[p]; v > 0.15 && v < 0.85 {
			detail += math.Abs(float64(v - base[p]))
			nd++
		}
		i := p * 3
		L, a, b := toOKLab(float64(img.Pix[i])*gain, float64(img.Pix[i+1])*gain, float64(img.Pix[i+2])*gain)
		if L > 0.2 && L < 0.9 {
			chroma += math.Hypot(a, b)
			nc++
		}
	}
	if nd > 0 {
		detail /= float64(nd)
	} else {
		detail = 0.02
	}
	if nc > 0 {
		chroma /= float64(nc)
	} else {
		chroma = 0.07
	}

	clarity := math.Round(clamp(5+(0.02-detail)*800, 0, 20))
	vibrance := math.Round(clamp((0.07-chroma)/0.07*35, 0, 30))
	p02 := math.Pow(percentile(lum, 0.02)*gain, 1/2.2)
	haze := clamp((p02-0.08)/0.15, 0, 1) * clamp((0.05-chroma)/0.05, 0, 1)
	dehaze := math.Round(haze * 35)

	notes := []string{fmt.Sprintf("Clarity %s · Dehaze %s · Vibrance %s", signedNum(clarity), signedNum(dehaze), signedNum(vibrance))}
	return Local{Clarity: clarity, Dehaze: dehaze}, vibrance, notes
}

func autoDetail(kind string, iso float64) (Detail, string) {
	var d Detail
	stops := 0.0
	if iso > 400 {
		stops = math.Log2(iso / 400)
	}
	if kind == "raw" {
		luma := math.Round(clamp(12*stops, 0, 60))
		chroma := math.Round(clamp(20+8*stops, 20, 60))
		d.Noise = Noise{Luma: luma, Chroma: chroma}
		d.Sharpen = Sharpen{Amount: 40, Radius: 1, Threshold: math.Round(clamp(10+luma/2, 10, 40))}
	} else {
		if iso >= 3200 {
			d.Noise = Noise{Luma: math.Round(clamp(8*math.Log2(iso/1600), 0, 30)), Chroma: 15}
		}
		d.Sharpen = Sharpen{Amount: 15, Radius: 0.8, Threshold: 20}
	}
	isoText := "ISO unknown"
	if iso > 0 {
		isoText = fmt.Sprintf("ISO %.0f", iso)
	}
	return d, fmt.Sprintf("Noise reduction %g / %g, sharpening %g (%s)", d.Noise.Luma, d.Noise.Chroma, d.Sharpen.Amount, isoText)
}

// autoLens enables lens correction when a profile is remembered for the lens
// or the best Lensfun candidate is unambiguous.
func (e *Engine) autoLens(ctx context.Context, src *Source, resolved Recipe, info *ExifLens) (*Lens, string) {
	if resolved.Lens.Enabled || src.Kind != "raw" || info.Lens == "" {
		return nil, ""
	}
	db := e.lenses()
	if len(db.lenses) == 0 {
		return nil, ""
	}
	if id, _ := e.confirmedLens(ctx, info.Lens); id != "" && db.byID[id] != nil {
		return &Lens{Enabled: true}, "Lens correction: remembered profile " + strings.Replace(id, "|", " ", 1)
	}
	mount := ""
	if cam := db.camera(info.Make, info.Model); cam != nil {
		mount = cam.mount
	}
	c := db.Candidates(info.Lens, info.Make, mount, 2)
	if len(c) == 0 || c[0].Score < autoLensScore || (len(c) > 1 && c[0].Score-c[1].Score < autoLensGap) {
		return nil, ""
	}
	return &Lens{Enabled: true, Profile: c[0].ID}, fmt.Sprintf("Lens correction: %s %s", c[0].Maker, c[0].Model)
}

// lowKeyLimit is the most Auto may brighten a low-key scene: from
// autoLowKeyBoost EV for very dark frames up to +autoLowKeyRange at autoLowKey.
func lowKeyLimit(key float64) float64 {
	t := clamp((key-autoDarkKey)/(autoLowKey-autoDarkKey), 0, 1)
	return autoLowKeyBoost + autoLowKeyRange*t
}
