package develop

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	exportTTL     = 24 * time.Hour
	exportTimeout = 15 * time.Minute
	enc16Bits     = 20
)

var (
	enc16Once sync.Once
	enc16LUT  []uint16
)

type ExportOptions struct {
	Format   string `json:"format"`
	LongSide int    `json:"long_side"`
	Quality  int    `json:"quality"`
	Space    string `json:"space"`
	Sharpen  string `json:"sharpen"`
	Metadata bool   `json:"metadata"`
}

type ExportJob struct {
	ID       string    `json:"id"`
	PhotoID  int       `json:"photo_id"`
	State    string    `json:"state"`
	Error    string    `json:"error,omitempty"`
	Filename string    `json:"filename"`
	Size     int64     `json:"size,omitempty"`
	Created  time.Time `json:"created"`
	path     string
}

// exportRegistry tracks export jobs in memory; finished files live in
// CACHE_DIR/exports for exportTTL and do not survive a restart's registry.
type exportRegistry struct {
	mu   sync.Mutex
	jobs map[string]*ExportJob
}

func (o *ExportOptions) normalize() error {
	o.Format = strings.ToLower(strings.TrimSpace(o.Format))
	if o.Format == "" {
		o.Format = "jpeg"
	}
	if o.Format != "jpeg" && o.Format != "tiff16" {
		return fmt.Errorf("%w: format %q", ErrInvalidRecipe, o.Format)
	}
	if o.Space == "" {
		o.Space = "srgb"
	}
	if o.Space != "srgb" && o.Space != "p3" {
		return fmt.Errorf("%w: color space %q", ErrInvalidRecipe, o.Space)
	}
	if o.Sharpen == "" {
		o.Sharpen = "none"
	}
	if o.Sharpen != "none" && o.Sharpen != "screen" && o.Sharpen != "print" {
		return fmt.Errorf("%w: sharpening %q", ErrInvalidRecipe, o.Sharpen)
	}
	if o.Quality == 0 {
		o.Quality = 92
	}
	o.Quality = clampInt(o.Quality, 50, 100)
	if o.LongSide < 0 {
		return fmt.Errorf("%w: negative size", ErrInvalidRecipe)
	}
	if o.LongSide > 0 && o.LongSide < 64 {
		o.LongSide = 64
	}
	return nil
}

func (r *exportRegistry) put(j *ExportJob) {
	r.mu.Lock()
	r.jobs[j.ID] = j
	r.mu.Unlock()
}

func (r *exportRegistry) update(id string, fn func(*ExportJob)) {
	r.mu.Lock()
	if j, ok := r.jobs[id]; ok {
		fn(j)
	}
	r.mu.Unlock()
}

func (r *exportRegistry) get(id string) (ExportJob, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	j, ok := r.jobs[id]
	if !ok {
		return ExportJob{}, false
	}
	return *j, true
}

func (r *exportRegistry) gc(ttl time.Duration, dir string) {
	keep := map[string]bool{}
	r.mu.Lock()
	for id, j := range r.jobs {
		if time.Since(j.Created) > ttl && j.State != "queued" && j.State != "running" {
			_ = os.Remove(j.path)
			delete(r.jobs, id)
			continue
		}
		keep[filepath.Base(j.path)] = true
	}
	r.mu.Unlock()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if keep[e.Name()] {
			continue
		}
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > ttl {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

func randomID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// StartExport renders the given (possibly unsaved) recipe in the background.
func (e *Engine) StartExport(ctx context.Context, id int, rec Recipe, opts ExportOptions) (*ExportJob, error) {
	if err := opts.normalize(); err != nil {
		return nil, err
	}
	resolved, src, err := e.prepare(ctx, id, rec)
	if err != nil {
		return nil, err
	}
	p, err := e.loadPhoto(ctx, id)
	if err != nil {
		return nil, err
	}
	ext := ".jpg"
	if opts.Format == "tiff16" {
		ext = ".tif"
	}
	dir := filepath.Join(e.cfg.CacheDir, "exports")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	jid := randomID()
	job := &ExportJob{
		ID:       jid,
		PhotoID:  id,
		State:    "queued",
		Filename: strings.TrimSuffix(p.Filename, filepath.Ext(p.Filename)) + ext,
		Created:  time.Now(),
		path:     filepath.Join(dir, jid+ext),
	}
	e.exports.put(job)
	go e.runExport(jid, job.path, src, resolved, opts)
	snapshot := *job
	return &snapshot, nil
}

func (e *Engine) ExportJob(id string) (*ExportJob, bool) {
	j, ok := e.exports.get(id)
	if !ok {
		return nil, false
	}
	return &j, true
}

func (e *Engine) ExportFile(id string) (string, string, bool) {
	j, ok := e.exports.get(id)
	if !ok || j.State != "done" {
		return "", "", false
	}
	return j.path, j.Filename, true
}

func (e *Engine) runExport(jid, path string, src *Source, r Recipe, opts ExportOptions) {
	ctx, cancel := context.WithTimeout(context.Background(), exportTimeout)
	defer cancel()
	// Exports share the render lock with publishing so two full-resolution
	// renders never hold memory at once.
	e.renderMu.Lock()
	defer e.renderMu.Unlock()
	e.exports.update(jid, func(j *ExportJob) { j.State = "running" })

	err := e.export(ctx, path, src, r, opts)
	e.exports.update(jid, func(j *ExportJob) {
		if err != nil {
			j.State = "failed"
			j.Error = err.Error()
			_ = os.Remove(j.path)
			return
		}
		j.State = "done"
		if fi, serr := os.Stat(j.path); serr == nil {
			j.Size = fi.Size()
		}
	})
	if err != nil {
		log.Printf("develop: export %s failed: %v", jid, err)
	}
}

func (e *Engine) export(ctx context.Context, path string, src *Source, r Recipe, opts ExportOptions) error {
	img, err := e.renderLinear(ctx, src, r)
	if err != nil {
		return err
	}
	if opts.LongSide > 0 {
		img = img.FitWithin(opts.LongSide, opts.LongSide)
	}
	switch opts.Sharpen {
	case "screen":
		sharpen(img, Sharpen{Amount: 35, Radius: 1, Threshold: 10}, renderCtx{}.forImage(img))
	case "print":
		sharpen(img, Sharpen{Amount: 60, Radius: 1, Threshold: 10}, renderCtx{}.forImage(img))
	}
	m, icc := srgbFromRec2020, iccSRGB
	if opts.Space == "p3" {
		m, icc = p3FromRec2020, iccP3
	}

	ext := filepath.Ext(path)
	tmp := strings.TrimSuffix(path, ext) + ".part" + ext
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if opts.Format == "tiff16" {
		err = writeTIFF16(f, img, m, icc)
	} else {
		err = writeJPEG(f, toEncoded8(img, m), opts.Quality, icc)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if opts.Metadata {
		if err := e.copyMetadata(ctx, src.Abs, tmp); err != nil {
			log.Printf("develop: export metadata: %v", err)
		}
	}
	return os.Rename(tmp, path)
}

// copyMetadata copies EXIF/XMP/IPTC from the source, minus GPS (the gallery
// strips location on import), embedded previews and the orientation, which
// the render has already applied.
func (e *Engine) copyMetadata(ctx context.Context, src, dst string) error {
	if e.dec.exiftool == "" {
		return nil
	}
	out, err := exec.CommandContext(ctx, e.dec.exiftool, "-overwrite_original",
		"-TagsFromFile", src, "-exif:all", "-xmp:all", "-iptc:all",
		"--Orientation", "--ThumbnailImage", "--PreviewImage", "--ExifImageWidth", "--ExifImageHeight", "--gps:all",
		"-Orientation#=1", dst).CombinedOutput()
	if err != nil {
		return fmt.Errorf("exiftool: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func writeJPEG(w io.Writer, img image.Image, quality int, icc []byte) error {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return err
	}
	data := buf.Bytes()
	if len(icc) == 0 || len(icc)+16 > 65535 {
		_, err := w.Write(data)
		return err
	}
	seg := make([]byte, 4, len(icc)+18)
	seg[0], seg[1] = 0xFF, 0xE2
	binary.BigEndian.PutUint16(seg[2:], uint16(len(icc)+16))
	seg = append(seg, "ICC_PROFILE\x00"...)
	seg = append(seg, 1, 1)
	seg = append(seg, icc...)
	for _, part := range [][]byte{data[:2], seg, data[2:]} {
		if _, err := w.Write(part); err != nil {
			return err
		}
	}
	return nil
}

func encode16(v float32) uint16 {
	enc16Once.Do(func() {
		n := 1 << enc16Bits
		enc16LUT = make([]uint16, n+1)
		for i := 0; i <= n; i++ {
			enc16LUT[i] = uint16(srgbEncode(float64(i)/float64(n))*65535 + 0.5)
		}
	})
	if !(v > 0) {
		return 0
	}
	if v >= 1 {
		return 65535
	}
	return enc16LUT[int(v*(1<<enc16Bits)+0.5)]
}

// writeTIFF16 writes an uncompressed little-endian 16-bit RGB TIFF with an
// embedded ICC profile.
func writeTIFF16(w io.Writer, img *Image, toOutput Mat3, icc []byte) error {
	const (
		nTags      = 15
		tASCII     = 2
		tShort     = 3
		tLong      = 4
		tRational  = 5
		tUndefined = 7
	)
	software := "PhotoDock\x00"
	ifdSize := 2 + nTags*12 + 4
	bpsOff := 8 + ifdSize
	xresOff := bpsOff + 6
	yresOff := xresOff + 8
	swOff := yresOff + 8
	iccOff := swOff + len(software)
	iccOff += iccOff % 2
	dataOff := iccOff + len(icc)
	dataOff += dataOff % 2

	bw := bufio.NewWriterSize(w, 1<<20)
	le := binary.LittleEndian
	var scratch [4]byte
	put16 := func(v uint16) {
		le.PutUint16(scratch[:2], v)
		_, _ = bw.Write(scratch[:2])
	}
	put32 := func(v uint32) {
		le.PutUint32(scratch[:], v)
		_, _ = bw.Write(scratch[:])
	}
	entry := func(tag, typ uint16, count, value int) {
		put16(tag)
		put16(typ)
		put32(uint32(count))
		put32(uint32(value))
	}
	written := 0
	pad := func(to int) {
		for ; written < to; written++ {
			_ = bw.WriteByte(0)
		}
	}

	_, _ = bw.WriteString("II")
	put16(42)
	put32(8)
	put16(nTags)
	entry(256, tLong, 1, img.W)
	entry(257, tLong, 1, img.H)
	entry(258, tShort, 3, bpsOff)
	entry(259, tShort, 1, 1)
	entry(262, tShort, 1, 2)
	entry(273, tLong, 1, dataOff)
	entry(277, tShort, 1, 3)
	entry(278, tLong, 1, img.H)
	entry(279, tLong, 1, img.W*img.H*6)
	entry(282, tRational, 1, xresOff)
	entry(283, tRational, 1, yresOff)
	entry(284, tShort, 1, 1)
	entry(296, tShort, 1, 2)
	entry(305, tASCII, len(software), swOff)
	entry(34675, tUndefined, len(icc), iccOff)
	put32(0)
	put16(16)
	put16(16)
	put16(16)
	put32(300)
	put32(1)
	put32(300)
	put32(1)
	_, _ = bw.WriteString(software)
	written = swOff + len(software)
	pad(iccOff)
	_, _ = bw.Write(icc)
	written += len(icc)
	pad(dataOff)

	m := toMat32(toOutput)
	row := make([]byte, img.W*6)
	for y := 0; y < img.H; y++ {
		for x := 0; x < img.W; x++ {
			i := (y*img.W + x) * 3
			r, g, b := img.Pix[i], img.Pix[i+1], img.Pix[i+2]
			o := x * 6
			le.PutUint16(row[o:], encode16(m[0]*r+m[1]*g+m[2]*b))
			le.PutUint16(row[o+2:], encode16(m[3]*r+m[4]*g+m[5]*b))
			le.PutUint16(row[o+4:], encode16(m[6]*r+m[7]*g+m[8]*b))
		}
		if _, err := bw.Write(row); err != nil {
			return err
		}
	}
	return bw.Flush()
}
