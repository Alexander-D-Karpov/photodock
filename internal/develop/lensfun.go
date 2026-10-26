package develop

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Lensfun XML support: r=1 is half the short side (distortion/TCA) or the corner (vignetting); no splines.

type lfText struct {
	Lang  string `xml:"lang,attr"`
	Value string `xml:",chardata"`
}

type lfDistortionXML struct {
	Model string  `xml:"model,attr"`
	Focal float64 `xml:"focal,attr"`
	K1    float64 `xml:"k1,attr"`
	K2    float64 `xml:"k2,attr"`
	A     float64 `xml:"a,attr"`
	B     float64 `xml:"b,attr"`
	C     float64 `xml:"c,attr"`
}

type lfTCAXML struct {
	Model string  `xml:"model,attr"`
	Focal float64 `xml:"focal,attr"`
	KR    float64 `xml:"kr,attr"`
	KB    float64 `xml:"kb,attr"`
	VR    float64 `xml:"vr,attr"`
	VB    float64 `xml:"vb,attr"`
	CR    float64 `xml:"cr,attr"`
	CB    float64 `xml:"cb,attr"`
	BR    float64 `xml:"br,attr"`
	BB    float64 `xml:"bb,attr"`
}

type lfVignettingXML struct {
	Model    string  `xml:"model,attr"`
	Focal    float64 `xml:"focal,attr"`
	Aperture float64 `xml:"aperture,attr"`
	Distance float64 `xml:"distance,attr"`
	K1       float64 `xml:"k1,attr"`
	K2       float64 `xml:"k2,attr"`
	K3       float64 `xml:"k3,attr"`
}

type lfCalibrationXML struct {
	CropFactor  float64           `xml:"cropfactor,attr"`
	AspectRatio string            `xml:"aspect-ratio,attr"`
	Distortion  []lfDistortionXML `xml:"distortion"`
	TCA         []lfTCAXML        `xml:"tca"`
	Vignetting  []lfVignettingXML `xml:"vignetting"`
}

type lfCenterXML struct {
	X float64 `xml:"x,attr"`
	Y float64 `xml:"y,attr"`
}

type lfLensXML struct {
	Maker       []lfText           `xml:"maker"`
	Model       []lfText           `xml:"model"`
	Mount       []string           `xml:"mount"`
	CropFactor  float64            `xml:"cropfactor"`
	AspectRatio string             `xml:"aspect-ratio"`
	Center      *lfCenterXML       `xml:"center"`
	Calibration []lfCalibrationXML `xml:"calibration"`
}

type lfCameraXML struct {
	Maker      []lfText `xml:"maker"`
	Model      []lfText `xml:"model"`
	Mount      string   `xml:"mount"`
	CropFactor float64  `xml:"cropfactor"`
}

type lfDatabaseXML struct {
	Cameras []lfCameraXML `xml:"camera"`
	Lenses  []lfLensXML   `xml:"lens"`
}

type lensSig struct {
	lo, hi, ap float64
	tokens     map[string]bool
}

type LensProfile struct {
	ID       string
	Maker    string
	Model    string
	Mounts   []string
	crop     float64
	aspect   float64
	centerX  float64
	centerY  float64
	dist     []lfDistortionXML
	tca      []lfTCAXML
	vig      []lfVignettingXML
	minFocal float64
	maxFocal float64
	sig      lensSig
}

type LensCandidate struct {
	ID    string  `json:"id"`
	Maker string  `json:"maker"`
	Model string  `json:"model"`
	Score float64 `json:"score"`
}

type lfCamera struct {
	maker string
	model string
	mount string
	crop  float64
}

type LensDB struct {
	dir         string
	fingerprint string
	lenses      []*LensProfile
	byID        map[string]*LensProfile
	cameras     []lfCamera
}

var (
	focalRe    = regexp.MustCompile(`(\d+(?:\.\d+)?)(?:\s*-\s*(\d+(?:\.\d+)?))?\s*mm`)
	apertureRe = regexp.MustCompile(`f(\d+(?:\.\d+)?)`)
	tokenRe    = regexp.MustCompile(`[a-z0-9]+(?:\.[0-9]+)?`)
)

func newLensDB() *LensDB {
	return &LensDB{byID: map[string]*LensProfile{}, fingerprint: "none"}
}

func detectLensfunDir() string {
	for _, d := range []string{
		"/usr/share/lensfun/version_2", "/usr/share/lensfun/version_1", "/usr/share/lensfun",
		"/usr/local/share/lensfun/version_2", "/usr/local/share/lensfun/version_1", "/usr/local/share/lensfun",
	} {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			return d
		}
	}
	return ""
}

func loadLensDB(dir string) *LensDB {
	db := newLensDB()
	db.dir = dir
	if dir == "" {
		return db
	}
	var files []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".xml") {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	h := sha256.New()
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		h.Write([]byte(f))
		h.Write(data)
		_ = db.addXML(data)
	}
	if len(files) > 0 {
		db.fingerprint = hex.EncodeToString(h.Sum(nil))[:12]
	}
	return db
}

func (db *LensDB) addXML(data []byte) error {
	var x lfDatabaseXML
	if err := xml.Unmarshal(data, &x); err != nil {
		return err
	}
	for _, c := range x.Cameras {
		model := normName(pickText(c.Model))
		if model == "" {
			continue
		}
		db.cameras = append(db.cameras, lfCamera{
			maker: normName(pickText(c.Maker)),
			model: model,
			mount: strings.TrimSpace(c.Mount),
			crop:  c.CropFactor,
		})
	}
	for _, l := range x.Lenses {
		p := newLensProfile(l)
		if p == nil {
			continue
		}
		if _, dup := db.byID[p.ID]; dup {
			continue
		}
		db.byID[p.ID] = p
		db.lenses = append(db.lenses, p)
	}
	return nil
}

func pickText(ts []lfText) string {
	var en, first string
	for _, t := range ts {
		v := strings.TrimSpace(t.Value)
		if v == "" {
			continue
		}
		if t.Lang == "" {
			return v
		}
		if t.Lang == "en" && en == "" {
			en = v
		}
		if first == "" {
			first = v
		}
	}
	if en != "" {
		return en
	}
	return first
}

func normName(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

func parseAspect(s string) float64 {
	s = strings.TrimSpace(s)
	r := 0.0
	if a, b, ok := strings.Cut(s, ":"); ok {
		x, _ := strconv.ParseFloat(strings.TrimSpace(a), 64)
		y, _ := strconv.ParseFloat(strings.TrimSpace(b), 64)
		if x > 0 && y > 0 {
			r = x / y
		}
	} else if v, err := strconv.ParseFloat(s, 64); err == nil {
		r = v
	}
	if r <= 0 {
		return 1.5
	}
	if r < 1 {
		r = 1 / r
	}
	return r
}

func newLensProfile(x lfLensXML) *LensProfile {
	maker, model := pickText(x.Maker), pickText(x.Model)
	if maker == "" || model == "" {
		return nil
	}
	p := &LensProfile{
		ID:     maker + "|" + model,
		Maker:  maker,
		Model:  model,
		crop:   x.CropFactor,
		aspect: parseAspect(x.AspectRatio),
	}
	for _, m := range x.Mount {
		if m = strings.TrimSpace(m); m != "" {
			p.Mounts = append(p.Mounts, m)
		}
	}
	if x.Center != nil {
		p.centerX, p.centerY = x.Center.X, x.Center.Y
	}
	for _, c := range x.Calibration {
		if c.CropFactor > 0 && p.crop <= 0 {
			p.crop = c.CropFactor
		}
		if c.AspectRatio != "" && x.AspectRatio == "" {
			p.aspect = parseAspect(c.AspectRatio)
		}
		for _, d := range c.Distortion {
			if d.Model == "poly3" || d.Model == "poly5" || d.Model == "ptlens" {
				p.dist = append(p.dist, d)
			}
		}
		for _, t := range c.TCA {
			if t.Model == "linear" || t.Model == "poly3" {
				p.tca = append(p.tca, t)
			}
		}
		for _, v := range c.Vignetting {
			if v.Model == "pa" && v.Aperture > 0 {
				p.vig = append(p.vig, v)
			}
		}
	}
	if len(p.dist)+len(p.tca)+len(p.vig) == 0 {
		return nil
	}
	if p.crop <= 0 {
		p.crop = 1
	}
	sort.Slice(p.dist, func(i, j int) bool { return p.dist[i].Focal < p.dist[j].Focal })
	sort.Slice(p.tca, func(i, j int) bool { return p.tca[i].Focal < p.tca[j].Focal })
	p.minFocal, p.maxFocal = math.Inf(1), math.Inf(-1)
	for _, f := range p.focals() {
		p.minFocal = math.Min(p.minFocal, f)
		p.maxFocal = math.Max(p.maxFocal, f)
	}
	p.sig = newLensSig(maker + " " + model)
	return p
}

func (p *LensProfile) focals() []float64 {
	var out []float64
	for _, d := range p.dist {
		out = append(out, d.Focal)
	}
	for _, t := range p.tca {
		out = append(out, t.Focal)
	}
	for _, v := range p.vig {
		out = append(out, v.Focal)
	}
	return out
}

func newLensSig(s string) lensSig {
	s = strings.ReplaceAll(strings.ToLower(s), "f/", "f")
	sig := lensSig{tokens: map[string]bool{}}
	if m := focalRe.FindStringSubmatch(s); m != nil {
		sig.lo, _ = strconv.ParseFloat(m[1], 64)
		sig.hi = sig.lo
		if m[2] != "" {
			sig.hi, _ = strconv.ParseFloat(m[2], 64)
		}
	}
	if m := apertureRe.FindStringSubmatch(s); m != nil {
		sig.ap, _ = strconv.ParseFloat(m[1], 64)
	}
	for _, t := range tokenRe.FindAllString(s, -1) {
		sig.tokens[t] = true
	}
	return sig
}

func matchScore(a, b lensSig) float64 {
	score := 0.0
	if a.lo > 0 && b.lo > 0 {
		if math.Abs(a.lo-b.lo) > 0.6 || math.Abs(a.hi-b.hi) > 0.6 {
			return 0
		}
		score++
	}
	if a.ap > 0 && b.ap > 0 {
		if math.Abs(a.ap-b.ap) < 0.05 {
			score += 0.5
		} else {
			score -= 0.5
		}
	}
	inter, union := 0, len(b.tokens)
	for t := range a.tokens {
		if b.tokens[t] {
			inter++
		} else {
			union++
		}
	}
	if union > 0 {
		score += float64(inter) / float64(union)
	}
	return score
}

func containsFold(list []string, v string) bool {
	for _, s := range list {
		if strings.EqualFold(s, v) {
			return true
		}
	}
	return false
}

func (db *LensDB) camera(make, model string) *lfCamera {
	mk, md := normName(make), normName(model)
	if md == "" {
		return nil
	}
	for i := range db.cameras {
		c := &db.cameras[i]
		if c.model == md && (mk == "" || c.maker == "" || strings.Contains(mk, c.maker) || strings.Contains(c.maker, mk)) {
			return c
		}
	}
	return nil
}

func (db *LensDB) Candidates(exifLens, make, mount string, limit int) []LensCandidate {
	out := []LensCandidate{}
	if strings.TrimSpace(exifLens) == "" {
		return out
	}
	sig := newLensSig(make + " " + exifLens)
	for _, p := range db.lenses {
		if mount != "" && len(p.Mounts) > 0 && !containsFold(p.Mounts, mount) {
			continue
		}
		if s := matchScore(sig, p.sig); s >= 0.35 {
			out = append(out, LensCandidate{ID: p.ID, Maker: p.Maker, Model: p.Model, Score: math.Round(s*100) / 100})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (db *LensDB) Search(q string, limit int) []LensCandidate {
	out := []LensCandidate{}
	qs := newLensSig(q)
	if len(qs.tokens) == 0 {
		return out
	}
	for _, p := range db.lenses {
		hit := 0
		for t := range qs.tokens {
			if p.sig.tokens[t] {
				hit++
				continue
			}
			for pt := range p.sig.tokens {
				if strings.HasPrefix(pt, t) {
					hit++
					break
				}
			}
		}
		if s := float64(hit) / float64(len(qs.tokens)); s >= 0.5 {
			out = append(out, LensCandidate{ID: p.ID, Maker: p.Maker, Model: p.Model, Score: s})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

type lensCoeffs struct {
	distModel string
	dist      [3]float64
	tcaModel  string
	red, blue [3]float64
	hasVig    bool
	vig       [3]float64
}

func bracket(n int, focalAt func(int) float64, f float64) (int, int, float64) {
	if n == 0 {
		return -1, -1, 0
	}
	if f <= focalAt(0) {
		return 0, 0, 0
	}
	if f >= focalAt(n-1) {
		return n - 1, n - 1, 0
	}
	for i := 0; i < n-1; i++ {
		a, b := focalAt(i), focalAt(i+1)
		if f >= a && f <= b {
			if b == a {
				return i, i, 0
			}
			return i, i + 1, (f - a) / (b - a)
		}
	}
	return n - 1, n - 1, 0
}

func lerp3(a, b [3]float64, t float64) [3]float64 {
	return [3]float64{a[0] + (b[0]-a[0])*t, a[1] + (b[1]-a[1])*t, a[2] + (b[2]-a[2])*t}
}

func distTerms(d lfDistortionXML) [3]float64 {
	switch d.Model {
	case "poly3":
		return [3]float64{d.K1, 0, 0}
	case "poly5":
		return [3]float64{d.K1, d.K2, 0}
	}
	return [3]float64{d.A, d.B, d.C}
}

func orOne(v float64) float64 {
	if v == 0 {
		return 1
	}
	return v
}

func tcaTerms(t lfTCAXML) ([3]float64, [3]float64) {
	if t.Model == "linear" {
		return [3]float64{orOne(t.KR), 0, 0}, [3]float64{orOne(t.KB), 0, 0}
	}
	return [3]float64{orOne(t.VR), t.CR, t.BR}, [3]float64{orOne(t.VB), t.CB, t.BB}
}

func (p *LensProfile) coefficients(focal, aperture, distance float64) lensCoeffs {
	var c lensCoeffs
	if i, j, t := bracket(len(p.dist), func(k int) float64 { return p.dist[k].Focal }, focal); i >= 0 {
		a, b := p.dist[i], p.dist[j]
		if a.Model != b.Model {
			if t > 0.5 {
				a = b
			}
			b, t = a, 0
		}
		c.distModel = a.Model
		c.dist = lerp3(distTerms(a), distTerms(b), t)
	}
	if i, j, t := bracket(len(p.tca), func(k int) float64 { return p.tca[k].Focal }, focal); i >= 0 {
		a, b := p.tca[i], p.tca[j]
		if a.Model != b.Model {
			if t > 0.5 {
				a = b
			}
			b, t = a, 0
		}
		ra, ba := tcaTerms(a)
		rb, bb := tcaTerms(b)
		c.tcaModel = a.Model
		c.red = lerp3(ra, rb, t)
		c.blue = lerp3(ba, bb, t)
	}
	if len(p.vig) > 0 {
		c.hasVig = true
		c.vig = p.interpolateVignetting(focal, aperture, distance)
	}
	return c
}

func (p *LensProfile) interpolateVignetting(focal, aperture, distance float64) [3]float64 {
	if aperture <= 0 {
		aperture = 8
	}
	if distance <= 0 {
		distance = 10
	}
	span := p.maxFocal - p.minFocal
	if span <= 0 {
		span = 1
	}
	var sum [3]float64
	wsum := 0.0
	for _, v := range p.vig {
		vd := v.Distance
		if vd <= 0 {
			vd = 1000
		}
		df := (focal - v.Focal) / span
		da := 4/aperture - 4/v.Aperture
		dd := 0.1/distance - 0.1/vd
		d := math.Sqrt(df*df + da*da + dd*dd)
		if d < 1e-5 {
			return [3]float64{v.K1, v.K2, v.K3}
		}
		w := 1 / math.Pow(d, 3.5)
		sum[0] += w * v.K1
		sum[1] += w * v.K2
		sum[2] += w * v.K3
		wsum += w
	}
	return [3]float64{sum[0] / wsum, sum[1] / wsum, sum[2] / wsum}
}

func distortionRatio(model string, k [3]float64) func(float64) float64 {
	switch model {
	case "poly3":
		return func(r float64) float64 { return 1 - k[0] + k[0]*r*r }
	case "poly5":
		return func(r float64) float64 { r2 := r * r; return 1 + k[0]*r2 + k[1]*r2*r2 }
	case "ptlens":
		return func(r float64) float64 { return k[0]*r*r*r + k[1]*r*r + k[2]*r + 1 - k[0] - k[1] - k[2] }
	}
	return nil
}

func tcaRatio(model string, k [3]float64) func(float64) float64 {
	switch model {
	case "linear":
		return func(float64) float64 { return k[0] }
	case "poly3":
		return func(r float64) float64 { return k[2]*r*r + k[1]*r + k[0] }
	}
	return nil
}

type lensModel struct {
	cx, cy       float64
	norm         float64
	scale        float64
	dist         func(float64) float64
	distStrength float64
	tcaR, tcaB   func(float64) float64
	hasVig       bool
	vig          [3]float64
	vigStrength  float64
	vigNorm      float64
}

func newLensModel(p *LensProfile, co lensCoeffs, srcW, srcH int, cameraCrop float64, l Lens) *lensModel {
	w, h := float64(srcW), float64(srcH)
	size := math.Min(w, h)
	iar := math.Max(w, h) / size
	calCrop := p.crop
	if cameraCrop <= 0 {
		cameraCrop = calCrop
	}
	arc := math.Sqrt(p.aspect*p.aspect + 1)
	cc := 1 / math.Sqrt(iar*iar+1) * calCrop / cameraCrop * arc
	m := &lensModel{
		norm:    2 / size * cc,
		cx:      w/2 + p.centerX*size/2,
		cy:      h/2 + p.centerY*size/2,
		scale:   1,
		vigNorm: 1 / arc,
	}
	if s := (100 + l.Distortion) / 100; s > 0 && co.distModel != "" {
		m.dist = distortionRatio(co.distModel, co.dist)
		m.distStrength = s
	}
	if !l.NoTCA && co.tcaModel != "" {
		m.tcaR = tcaRatio(co.tcaModel, co.red)
		m.tcaB = tcaRatio(co.tcaModel, co.blue)
	}
	if s := (100 + l.Vignetting) / 100; s > 0 && co.hasVig {
		m.hasVig = true
		m.vig = co.vig
		m.vigStrength = s
	}
	if m.dist == nil && m.tcaR == nil && !m.hasVig {
		return nil
	}
	if m.dist != nil {
		m.autoScale(w, h)
	}
	return m
}

func (m *lensModel) lookup(sx, sy float64) (xr, yr, xg, yg, xb, yb, gain float64) {
	nx := (sx - m.cx) * m.norm / m.scale
	ny := (sy - m.cy) * m.norm / m.scale
	r := math.Hypot(nx, ny)
	k := 1.0
	if m.dist != nil {
		k = 1 + (m.dist(r)-1)*m.distStrength
	}
	dx, dy, rd := nx*k, ny*k, r*k
	kr, kb := 1.0, 1.0
	if m.tcaR != nil {
		kr, kb = m.tcaR(rd), m.tcaB(rd)
	}
	xg, yg = dx/m.norm+m.cx, dy/m.norm+m.cy
	xr, yr = dx*kr/m.norm+m.cx, dy*kr/m.norm+m.cy
	xb, yb = dx*kb/m.norm+m.cx, dy*kb/m.norm+m.cy
	gain = 1
	if m.hasVig {
		rv := rd * m.vigNorm
		r2 := rv * rv
		v := 1 + m.vig[0]*r2 + m.vig[1]*r2*r2 + m.vig[2]*r2*r2*r2
		if v > 0.05 {
			gain = 1 + (1/v-1)*m.vigStrength
		}
	}
	return
}

// autoScale zooms in just enough that the corrected frame has no empty edges.
func (m *lensModel) autoScale(w, h float64) {
	inside := func(s float64) bool {
		m.scale = s
		const steps = 32
		for i := 0; i <= steps; i++ {
			t := float64(i) / steps
			for _, pt := range [4][2]float64{{t * w, 0}, {t * w, h}, {0, t * h}, {w, t * h}} {
				xr, yr, xg, yg, xb, yb, _ := m.lookup(pt[0], pt[1])
				for _, c := range [3][2]float64{{xr, yr}, {xg, yg}, {xb, yb}} {
					// Strict bounds: any slack lets the search zoom out past 1 and shift an undistorted frame.
					if c[0] < 0 || c[1] < 0 || c[0] > w || c[1] > h {
						return false
					}
				}
			}
		}
		return true
	}
	lo, hi := 0.5, 3.0
	if !inside(hi) {
		m.scale = 1
		return
	}
	for i := 0; i < 40; i++ {
		mid := (lo + hi) / 2
		if inside(mid) {
			hi = mid
		} else {
			lo = mid
		}
	}
	m.scale = hi
}
