package develop

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/disintegration/imaging"
	"golang.org/x/image/tiff"
)

const (
	ColorRaw     = 0
	ColorXYZ     = 5
	ColorRec2020 = 8
)

type RawInfo struct {
	Make        string
	Model       string
	Orientation int
	AsShot      [3]float64
	Daylight    [3]float64
}

type DecodeOptions struct {
	Demosaic   string
	Highlights string
	Mul        [3]float64
	Half       bool
	ColorSpace int
}

type Decoder struct {
	cacheDir    string
	dcraw       string
	rawIdentify string
	exiftool    string

	verOnce sync.Once
	ver     string

	mu     sync.Mutex
	infos  map[string]*RawInfo
	lenses map[string]*ExifLens
	cams   map[string]Mat3
	locks  sync.Map
}

func NewDecoder(cacheDir string) *Decoder {
	d := &Decoder{
		cacheDir: cacheDir,
		infos:    map[string]*RawInfo{},
		lenses:   map[string]*ExifLens{},
		cams:     map[string]Mat3{},
	}
	d.dcraw, _ = exec.LookPath("dcraw_emu")
	d.rawIdentify, _ = exec.LookPath("raw-identify")
	d.exiftool, _ = exec.LookPath("exiftool")
	return d
}

func (d *Decoder) Available() bool {
	return d.dcraw != ""
}

func (d *Decoder) keyLock(key string) *sync.Mutex {
	v, _ := d.locks.LoadOrStore(key, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// LibRawVersion identifies the LibRaw build for render hashes; dcraw_emu 0.22 has no version, so it falls back to path/size/mtime.
func (d *Decoder) LibRawVersion() string {
	d.verOnce.Do(func() {
		if d.dcraw == "" {
			d.ver = "none"
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, _ := exec.CommandContext(ctx, d.dcraw).CombinedOutput()
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "LibRaw") {
				d.ver = strings.TrimSpace(line)
				return
			}
		}
		if fi, err := os.Stat(d.dcraw); err == nil {
			d.ver = fmt.Sprintf("%s:%d:%d", d.dcraw, fi.Size(), fi.ModTime().Unix())
			return
		}
		d.ver = "unknown"
	})
	return d.ver
}

func (d *Decoder) RawInfo(ctx context.Context, path, fingerprint string) (*RawInfo, error) {
	d.mu.Lock()
	if ri, ok := d.infos[fingerprint]; ok {
		d.mu.Unlock()
		return ri, nil
	}
	d.mu.Unlock()

	info := &RawInfo{Orientation: 1}
	if d.exiftool != "" {
		out, err := exec.CommandContext(ctx, d.exiftool, "-j", "-n", "-Make", "-Model", "-Orientation", "-WB_RGGBLevels", path).Output()
		if err == nil {
			var res []map[string]any
			if json.Unmarshal(out, &res) == nil && len(res) > 0 {
				m := res[0]
				info.Make = anyString(m["Make"])
				info.Model = anyString(m["Model"])
				if o, ok := m["Orientation"].(float64); ok && o >= 1 && o <= 8 {
					info.Orientation = int(o)
				}
				if f := parseFloats(anyString(m["WB_RGGBLevels"])); len(f) == 4 {
					info.AsShot = [3]float64{f[0], (f[1] + f[2]) / 2, f[3]}
				}
			}
		}
	}
	if d.rawIdentify != "" {
		out, err := exec.CommandContext(ctx, d.rawIdentify, "-v", path).Output()
		if err == nil {
			asShot, daylight := parseRawIdentify(string(out))
			if validMul(asShot) {
				info.AsShot = asShot
			}
			info.Daylight = daylight
		}
	}
	if !validMul(info.AsShot) {
		return nil, fmt.Errorf("no white balance multipliers for %s", filepath.Base(path))
	}
	info.AsShot = normalizeMul(info.AsShot)
	if validMul(info.Daylight) {
		info.Daylight = normalizeMul(info.Daylight)
	} else {
		info.Daylight = info.AsShot
	}

	d.mu.Lock()
	d.infos[fingerprint] = info
	d.mu.Unlock()
	return info, nil
}

// parseRawIdentify extracts WB multipliers from `raw-identify -v` (old multiplier lines or 0.22's Makernotes table); as-shot is zero if absent.
func parseRawIdentify(out string) (asShot, daylight [3]float64) {
	var tableShot, tableDay, camDay, derived [3]float64
	for _, line := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok {
			f := parseFloats(v)
			if len(f) < 3 {
				continue
			}
			mul := [3]float64{f[0], f[1], f[2]}
			if !validMul(mul) {
				continue
			}
			switch strings.TrimSpace(k) {
			case "Camera multipliers":
				asShot = mul
			case "Daylight multipliers":
				camDay = mul
			case "Derived D65 multipliers":
				derived = mul
			}
			continue
		}
		label, f := splitWBRow(line)
		if len(f) < 3 {
			continue
		}
		mul := [3]float64{f[0], f[1], f[2]}
		if !validMul(mul) {
			continue
		}
		switch label {
		case "As shot":
			tableShot = mul
		case "Daylight":
			tableDay = mul
		}
	}
	if !validMul(asShot) {
		asShot = tableShot
	}
	for _, m := range [][3]float64{tableDay, camDay, derived} {
		if validMul(m) {
			daylight = m
			break
		}
	}
	return asShot, daylight
}

// splitWBRow splits "  As shot   2720 1024 1824 1024   1.41 ..." into the leading label and the numbers that follow.
func splitWBRow(line string) (string, []float64) {
	fields := strings.Fields(line)
	i := 0
	for i < len(fields) {
		if _, err := strconv.ParseFloat(fields[i], 64); err == nil {
			break
		}
		i++
	}
	if i == 0 || i == len(fields) {
		return "", nil
	}
	return strings.Join(fields[:i], " "), parseFloats(strings.Join(fields[i:], " "))
}

func anyString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return fmt.Sprint(v)
}

func parseFloats(s string) []float64 {
	var out []float64
	for _, f := range strings.Fields(s) {
		if v, err := strconv.ParseFloat(f, 64); err == nil {
			out = append(out, v)
		}
	}
	return out
}

func validMul(m [3]float64) bool {
	return m[0] > 0 && m[1] > 0 && m[2] > 0
}

func normalizeMul(m [3]float64) [3]float64 {
	return [3]float64{m[0] / m[1], 1, m[2] / m[1]}
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', 6, 64)
}

func (d *Decoder) DecodeRaw(ctx context.Context, src string, o DecodeOptions) (*Image, error) {
	if d.dcraw == "" {
		return nil, errors.New("dcraw_emu is not installed")
	}
	if !validMul(o.Mul) {
		return nil, errors.New("invalid white balance multipliers")
	}
	q, ok := demosaicCodes[o.Demosaic]
	if !ok {
		q = demosaicCodes["ahd"]
	}
	hl := highlightCodes[o.Highlights]

	scratch := filepath.Join(d.cacheDir, "raw")
	if err := os.MkdirAll(scratch, 0755); err != nil {
		return nil, err
	}
	work, err := os.MkdirTemp(scratch, "dev-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(work) }()

	link := filepath.Join(work, "in"+strings.ToLower(filepath.Ext(src)))
	if err := os.Symlink(src, link); err != nil {
		return nil, err
	}

	args := []string{
		"-4", "-T", "-t", "0",
		"-o", strconv.Itoa(o.ColorSpace),
		"-q", strconv.Itoa(q),
		"-H", strconv.Itoa(hl),
		"-r", formatFloat(o.Mul[0]), formatFloat(o.Mul[1]), formatFloat(o.Mul[2]), formatFloat(o.Mul[1]),
	}
	if o.Half {
		args = append(args, "-h")
	}
	args = append(args, link)

	cmd := exec.CommandContext(ctx, d.dcraw, args...)
	cmd.Dir = work
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, fmt.Errorf("dcraw_emu: %w (%s)", err, msg)
	}
	return readTIFF16(link + ".tiff")
}

func readTIFF16(path string) (*Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	src, err := tiff.Decode(bufio.NewReaderSize(f, 1<<20))
	if err != nil {
		return nil, err
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	out := NewImage(w, h)
	const inv = 1.0 / 65535.0

	readPix := func(pix []byte, stride int) {
		parallelRows(h, func(y0, y1 int) {
			for y := y0; y < y1; y++ {
				row := pix[y*stride:]
				for x := 0; x < w; x++ {
					s := x * 8
					o := (y*w + x) * 3
					out.Pix[o] = float32(uint16(row[s])<<8|uint16(row[s+1])) * inv
					out.Pix[o+1] = float32(uint16(row[s+2])<<8|uint16(row[s+3])) * inv
					out.Pix[o+2] = float32(uint16(row[s+4])<<8|uint16(row[s+5])) * inv
				}
			}
		})
	}

	switch m := src.(type) {
	case *image.RGBA64:
		readPix(m.Pix, m.Stride)
	case *image.NRGBA64:
		readPix(m.Pix, m.Stride)
	default:
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				r, g, bb, _ := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
				o := (y*w + x) * 3
				out.Pix[o] = float32(r) * inv
				out.Pix[o+1] = float32(g) * inv
				out.Pix[o+2] = float32(bb) * inv
			}
		}
	}
	return out, nil
}

func (d *Decoder) DecodeRaster(path string) (*Image, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	src, err := imaging.Decode(bytes.NewReader(data), imaging.AutoOrientation(true))
	if err != nil {
		return nil, "", err
	}
	prof, warn := profileFor(data)
	n := imaging.Clone(src)
	w, h := n.Bounds().Dx(), n.Bounds().Dy()
	out := NewImage(w, h)
	m := toMat32(prof.toRec2020)
	parallelRows(h, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			row := n.Pix[y*n.Stride:]
			for x := 0; x < w; x++ {
				s := x * 4
				r := prof.lut[0][row[s]]
				g := prof.lut[1][row[s+1]]
				b := prof.lut[2][row[s+2]]
				o := (y*w + x) * 3
				out.Pix[o] = m[0]*r + m[1]*g + m[2]*b
				out.Pix[o+1] = m[3]*r + m[4]*g + m[5]*b
				out.Pix[o+2] = m[6]*r + m[7]*g + m[8]*b
			}
		}
	})
	return out, warn, nil
}

func (d *Decoder) RasterProfileWarning(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	_, warn := profileFor(data)
	return warn
}

func (d *Decoder) CameraMatrix(ctx context.Context, path string, info *RawInfo) (Mat3, error) {
	key := info.Make + "|" + info.Model + "|" + d.LibRawVersion()

	d.mu.Lock()
	if m, ok := d.cams[key]; ok {
		d.mu.Unlock()
		return m, nil
	}
	d.mu.Unlock()

	lock := d.keyLock("cam:" + key)
	lock.Lock()
	defer lock.Unlock()

	d.mu.Lock()
	if m, ok := d.cams[key]; ok {
		d.mu.Unlock()
		return m, nil
	}
	d.mu.Unlock()

	sum := sha256.Sum256([]byte(key))
	file := filepath.Join(d.cacheDir, "develop-meta", "cameras", hex.EncodeToString(sum[:8])+".json")
	if b, err := os.ReadFile(file); err == nil {
		var m Mat3
		if json.Unmarshal(b, &m) == nil {
			d.mu.Lock()
			d.cams[key] = m
			d.mu.Unlock()
			return m, nil
		}
	}

	opts := DecodeOptions{Demosaic: "ahd", Highlights: "clip", Mul: info.Daylight, Half: true, ColorSpace: ColorRaw}
	cam, err := d.DecodeRaw(ctx, path, opts)
	if err != nil {
		return Mat3{}, err
	}
	opts.ColorSpace = ColorXYZ
	xyz, err := d.DecodeRaw(ctx, path, opts)
	if err != nil {
		return Mat3{}, err
	}
	m, err := solveCameraMatrix(cam, xyz, info.Daylight)
	if err != nil {
		return Mat3{}, err
	}

	if err := os.MkdirAll(filepath.Dir(file), 0755); err == nil {
		if b, err := json.Marshal(m); err == nil {
			_ = os.WriteFile(file, b, 0644)
		}
	}
	d.mu.Lock()
	d.cams[key] = m
	d.mu.Unlock()
	return m, nil
}

type ExifLens struct {
	Make     string
	Model    string
	Lens     string
	Focal    float64
	Focal35  float64
	Aperture float64
	Distance float64
	ISO      float64
}

func (l *ExifLens) CropFactor() float64 {
	if l.Focal > 0 && l.Focal35 > 0 {
		return l.Focal35 / l.Focal
	}
	return 0
}

func anyFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case string:
		if f := parseFloats(t); len(f) > 0 {
			return f[0]
		}
	}
	return 0
}

func onlyDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// LensInfo reads the lens identity and shooting parameters lens correction needs.
func (d *Decoder) LensInfo(ctx context.Context, path, fingerprint string) *ExifLens {
	d.mu.Lock()
	if li, ok := d.lenses[fingerprint]; ok {
		d.mu.Unlock()
		return li
	}
	d.mu.Unlock()

	li := &ExifLens{}
	if d.exiftool != "" {
		out, err := exec.CommandContext(ctx, d.exiftool, "-j",
			"-Make", "-Model", "-LensModel", "-LensID",
			"-FocalLength#", "-FNumber#", "-FocalLengthIn35mmFormat#", "-ISO#",
			"-FocusDistance2#", "-FocusDistance#", "-ApproximateFocusDistance#", path).Output()
		if err == nil {
			var res []map[string]any
			if json.Unmarshal(out, &res) == nil && len(res) > 0 {
				m := res[0]
				li.Make = anyString(m["Make"])
				li.Model = anyString(m["Model"])
				li.Lens = anyString(m["LensModel"])
				if li.Lens == "" || strings.Trim(li.Lens, "-") == "" || onlyDigits(li.Lens) {
					li.Lens = anyString(m["LensID"])
				}
				if onlyDigits(li.Lens) || strings.HasPrefix(strings.ToLower(li.Lens), "unknown") {
					li.Lens = ""
				}
				li.Focal = anyFloat(m["FocalLength"])
				li.Focal35 = anyFloat(m["FocalLengthIn35mmFormat"])
				li.Aperture = anyFloat(m["FNumber"])
				li.ISO = anyFloat(m["ISO"])
				for _, k := range []string{"FocusDistance2", "FocusDistance", "ApproximateFocusDistance"} {
					if v := anyFloat(m[k]); v > 0 {
						li.Distance = v
						break
					}
				}
			}
		}
	}
	d.mu.Lock()
	d.lenses[fingerprint] = li
	d.mu.Unlock()
	return li
}
