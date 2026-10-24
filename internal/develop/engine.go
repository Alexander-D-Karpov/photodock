package develop

import (
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Alexander-D-Karpov/photodock/internal/config"
	"github.com/Alexander-D-Karpov/photodock/internal/database"
	"github.com/Alexander-D-Karpov/photodock/internal/services"
	"github.com/disintegration/imaging"
	"github.com/jackc/pgx/v5"
)

const (
	maxRenderAttempts = 3
	historyLimit      = 100
	gcGrace           = 10 * time.Minute
	proxyCacheEntries = 4
)

var (
	ErrNotFound          = errors.New("photo not found")
	ErrConflict          = errors.New("recipe revision conflict")
	ErrSourceUnavailable = errors.New("source file unavailable")

	hashPattern    = regexp.MustCompile(`^[0-9a-f]{16}$`)
	publishedNames = map[string]bool{
		"full.jpg": true, "small.jpg": true, "medium.jpg": true, "large.jpg": true, "placeholder.png": true,
	}
)

type derivative struct {
	name    string
	width   int
	quality int
}

var derivatives = []derivative{
	{"large.jpg", 2560, 90},
	{"medium.jpg", 1000, 86},
	{"small.jpg", 300, 80},
}

type Config struct {
	MediaRoot      string
	CacheDir       string
	ProxySize      int
	PreviewWorkers int
	LensfunDir     string
}

type Engine struct {
	db       *database.DB
	thumbs   *services.ThumbnailService
	cfg      Config
	dec      *Decoder
	sem      chan struct{}
	renderMu sync.Mutex
	wake     chan struct{}
	proxies  *proxyCache
	full     fullCache
	lensOnce sync.Once
	lensDB   *LensDB
	exports  exportRegistry
}

type Source struct {
	Abs         string
	Ext         string
	Kind        string
	Size        int64
	Fingerprint string
}

type SourceInfo struct {
	Available bool   `json:"available"`
	Kind      string `json:"kind,omitempty"`
	Format    string `json:"format,omitempty"`
	Size      int64  `json:"size,omitempty"`
	Error     string `json:"error,omitempty"`
}

type WhiteBalance struct {
	Temp float64 `json:"temp"`
	Tint float64 `json:"tint"`
}

type Capabilities struct {
	RawWhiteBalance bool `json:"raw_white_balance"`
	Demosaic        bool `json:"demosaic"`
	LensCorrection  bool `json:"lens_correction"`
}

type State struct {
	PhotoID         int           `json:"photo_id"`
	StagedID        string        `json:"staged_id,omitempty"`
	Source          SourceInfo    `json:"source"`
	Active          bool          `json:"active"`
	Recipe          Recipe        `json:"recipe"`
	Defaults        Recipe        `json:"defaults"`
	Revision        int64         `json:"revision"`
	DefaultsVersion int           `json:"defaults_version"`
	DesiredHash     string        `json:"desired_hash,omitempty"`
	PublishedHash   string        `json:"published_hash,omitempty"`
	RenderState     string        `json:"render_state"`
	RenderError     string        `json:"render_error,omitempty"`
	AsShot          *WhiteBalance `json:"as_shot,omitempty"`
	Capabilities    Capabilities  `json:"capabilities"`
	Lens            *LensState    `json:"lens,omitempty"`
	Warnings        []string      `json:"warnings,omitempty"`
}

type loaded struct {
	img         *Image
	orientation int
	raw         bool
	warning     string
	fullW       int
	fullH       int
}

func NewFromConfig(db *database.DB, thumbs *services.ThumbnailService, cfg *config.Config) *Engine {
	return New(db, thumbs, Config{
		MediaRoot:      cfg.MediaRoot,
		CacheDir:       cfg.CacheDir,
		ProxySize:      cfg.DevelopProxySize,
		PreviewWorkers: cfg.DevelopPreviewWorkers,
		LensfunDir:     cfg.LensfunDir,
	})
}

func New(db *database.DB, thumbs *services.ThumbnailService, c Config) *Engine {
	if c.ProxySize <= 0 {
		c.ProxySize = 2048
	}
	if c.PreviewWorkers <= 0 {
		c.PreviewWorkers = 2
	}
	return &Engine{
		db:      db,
		thumbs:  thumbs,
		cfg:     c,
		dec:     NewDecoder(c.CacheDir),
		sem:     make(chan struct{}, c.PreviewWorkers),
		wake:    make(chan struct{}, 1),
		proxies: newProxyCache(proxyCacheEntries),
		exports: exportRegistry{jobs: map[string]*ExportJob{}},
	}
}

func (e *Engine) kick() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

func (e *Engine) resolveSource(p *photoRow) (*Source, error) {
	clean := filepath.Clean(p.Path)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("%w: unsafe path", ErrSourceUnavailable)
	}
	return sourceFromAbs(filepath.Join(e.cfg.MediaRoot, clean), clean)
}

func (e *Engine) rawParams(ctx context.Context, src *Source, r Recipe) ([3]float64, int, error) {
	info, err := e.dec.RawInfo(ctx, src.Abs, src.Fingerprint)
	if err != nil {
		return [3]float64{}, 0, err
	}
	if r.WB.Mode != WBCustom {
		return info.AsShot, info.Orientation, nil
	}
	camToXYZ, err := e.dec.CameraMatrix(ctx, src.Abs, info)
	if err != nil {
		return [3]float64{}, 0, err
	}
	x, y := TempTintToXY(r.WB.Temp, r.WB.Tint)
	mul, err := mulsForWhite(camToXYZ, xyToXYZ(x, y))
	return mul, info.Orientation, err
}

func (e *Engine) load(ctx context.Context, src *Source, r Recipe, proxy bool) (*loaded, error) {
	started := time.Now()
	defer func() { timingsFrom(ctx).Since("decode", started) }()

	if src.Kind != "raw" {
		img, warn, err := e.dec.DecodeRaster(src.Abs)
		if err != nil {
			return nil, err
		}
		fw, fh := img.W, img.H
		if proxy {
			img = img.FitWithin(e.cfg.ProxySize, e.cfg.ProxySize)
		}
		return &loaded{img: img, orientation: 1, warning: warn, fullW: fw, fullH: fh}, nil
	}
	if !e.dec.Available() {
		return nil, fmt.Errorf("%w: dcraw_emu is not installed", ErrSourceUnavailable)
	}
	mul, orientation, err := e.rawParams(ctx, src, r)
	if err != nil {
		return nil, err
	}
	img, err := e.dec.DecodeRaw(ctx, src.Abs, DecodeOptions{
		Demosaic:   r.Raw.Demosaic,
		Highlights: r.Raw.Highlights,
		Mul:        mul,
		Half:       proxy,
		ColorSpace: ColorRec2020,
	})
	if err != nil {
		return nil, err
	}
	fw, fh := img.W, img.H
	if proxy {
		fw, fh = img.W*2, img.H*2
		img = img.FitWithin(e.cfg.ProxySize, e.cfg.ProxySize)
	}
	return &loaded{img: img, orientation: orientation, raw: true, fullW: fw, fullH: fh}, nil
}

func (e *Engine) proxy(ctx context.Context, src *Source, r Recipe) (*loaded, error) {
	key, err := e.decodeKey(ctx, src, r)
	if err != nil {
		return nil, err
	}
	key += "|proxy"
	if l, ok := e.proxies.get(key); ok {
		return l, nil
	}
	lock := e.dec.keyLock(key)
	lock.Lock()
	defer lock.Unlock()
	if l, ok := e.proxies.get(key); ok {
		return l, nil
	}
	l, err := e.load(ctx, src, r, true)
	if err != nil {
		return nil, err
	}
	e.proxies.put(key, l)
	return l, nil
}

func (e *Engine) defaultsVersionFor(ctx context.Context, id int) (int, error) {
	row, err := e.loadEdit(ctx, id)
	if err != nil {
		return 0, err
	}
	if row != nil {
		return row.DefaultsVersion, nil
	}
	return CurrentDefaultsVersion, nil
}

func (e *Engine) asShotWhite(ctx context.Context, src *Source) ([3]float64, error) {
	info, err := e.dec.RawInfo(ctx, src.Abs, src.Fingerprint)
	if err != nil {
		return [3]float64{}, err
	}
	camToXYZ, err := e.dec.CameraMatrix(ctx, src.Abs, info)
	if err != nil {
		return [3]float64{}, err
	}
	w := whiteOfMuls(camToXYZ, info.AsShot)
	if w[1] <= 0 {
		return [3]float64{}, errors.New("invalid as-shot white point")
	}
	return [3]float64{w[0] / w[1], 1, w[2] / w[1]}, nil
}

func (e *Engine) asShot(ctx context.Context, src *Source) (*WhiteBalance, error) {
	w := whiteD65
	if src.Kind == "raw" {
		var err error
		if w, err = e.asShotWhite(ctx, src); err != nil {
			return nil, err
		}
	}
	t, tint := XYToTempTint(xyzToXY(w))
	return &WhiteBalance{Temp: math.Round(t), Tint: roundTo(tint, 0.1)}, nil
}

func (e *Engine) State(ctx context.Context, id int) (*State, error) {
	p, err := e.loadPhoto(ctx, id)
	if err != nil {
		return nil, err
	}
	row, err := e.loadEdit(ctx, id)
	if err != nil {
		return nil, err
	}
	dv := CurrentDefaultsVersion
	if row != nil {
		dv = row.DefaultsVersion
	}
	defs, err := Defaults(dv)
	if err != nil {
		return nil, err
	}
	st := &State{PhotoID: id, Defaults: defs, DefaultsVersion: dv, RenderState: "none"}
	if row != nil {
		st.Active = true
		st.Recipe = row.Recipe
		st.Revision = row.Revision
		st.DesiredHash = row.DesiredHash
		st.PublishedHash = row.PublishedHash
		st.RenderState = row.RenderState
		st.RenderError = row.RenderError
	} else {
		st.Recipe = Recipe{Geometry: Geometry{Rotate: NormalizeRotation(p.Rotation)}}
	}
	src, err := e.resolveSource(p)
	if err != nil {
		st.Source = SourceInfo{Available: false, Error: err.Error()}
		return st, nil
	}
	e.fillSourceState(ctx, st, src)
	return st, nil
}

func (e *Engine) Save(ctx context.Context, id int, rec Recipe, expected int64, label string) (*State, error) {
	if err := e.saveRecipe(ctx, id, rec, expected, label, true); err != nil {
		return nil, err
	}
	e.kick()
	return e.State(ctx, id)
}

func (e *Engine) Reset(ctx context.Context, id int) error {
	tx, err := e.db.Pool().Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		UPDATE photos p SET edit_hash = NULL, width = pe.source_width, height = pe.source_height,
			blurhash = NULLIF(pe.source_blurhash, ''), updated_at = NOW()
		FROM photo_edits pe WHERE pe.photo_id = p.id AND p.id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `DELETE FROM photo_edits WHERE photo_id = $1`, id); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	_ = os.RemoveAll(e.thumbs.DevelopDir(id))
	return nil
}

func (e *Engine) PublishedFile(id int, hash, name string) (string, bool) {
	if !hashPattern.MatchString(hash) || !publishedNames[name] {
		return "", false
	}
	p := filepath.Join(e.thumbs.DevelopDir(id), hash, name)
	fi, err := os.Stat(p)
	if err != nil || fi.Size() == 0 {
		return "", false
	}
	return p, true
}

func (e *Engine) Start(ctx context.Context) {
	go func() {
		// A job still marked 'rendering' was interrupted by a restart, possibly
		// an OOM kill caused by that very render. Count it as an attempt so a
		// render that kills the process cannot crash-loop the service forever.
		if _, err := e.db.Pool().Exec(ctx, `
			UPDATE photo_edits SET render_attempts = render_attempts + 1,
				render_state = CASE WHEN render_attempts + 1 >= $1 THEN 'failed' ELSE 'queued' END,
				render_error = 'interrupted by server restart during render'
			WHERE render_state = 'rendering'`, maxRenderAttempts); err != nil {
			log.Printf("develop: reset rendering jobs: %v", err)
		}
		if n, err := e.Rehash(ctx, false); err != nil {
			log.Printf("develop: rehash: %v", err)
		} else if n > 0 {
			log.Printf("develop: %d edited photos queued after engine/source change", n)
		}
		e.gc(ctx)

		tick := time.NewTicker(30 * time.Second)
		defer tick.Stop()
		gcTick := time.NewTicker(gcGrace)
		defer gcTick.Stop()

		for {
			for ctx.Err() == nil {
				worked, err := e.processNext(ctx)
				if err != nil {
					if ctx.Err() == nil {
						log.Printf("develop: queue: %v", err)
					}
					break
				}
				if !worked {
					break
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-e.wake:
			case <-tick.C:
			case <-gcTick.C:
				e.gc(ctx)
			}
		}
	}()
}

func (e *Engine) processNext(ctx context.Context) (bool, error) {
	var id, dv, attempts int
	var raw []byte
	var hash string
	err := e.db.Pool().QueryRow(ctx, `
		SELECT photo_id, recipe, defaults_version, desired_hash, render_attempts FROM photo_edits
		WHERE published_hash IS DISTINCT FROM desired_hash AND render_state = 'queued' AND next_attempt_at <= NOW()
		ORDER BY priority DESC, updated_at LIMIT 1`).Scan(&id, &raw, &dv, &hash, &attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	tag, err := e.db.Pool().Exec(ctx, `
		UPDATE photo_edits SET render_state = 'rendering'
		WHERE photo_id = $1 AND desired_hash = $2 AND render_state = 'queued'`, id, hash)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return true, nil
	}

	var rec Recipe
	if err := json.Unmarshal(raw, &rec); err != nil {
		e.markFailed(ctx, id, hash, attempts, err)
		return true, nil
	}

	started := time.Now()
	if err := e.renderAndPublish(ctx, id, rec, dv, hash); err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		log.Printf("develop: photo %d render %s failed: %v", id, hash, err)
		e.markFailed(ctx, id, hash, attempts, err)
		return true, nil
	}
	log.Printf("develop: photo %d published %s in %s", id, hash, time.Since(started).Round(time.Millisecond))
	return true, nil
}

func (e *Engine) markFailed(ctx context.Context, id int, hash string, attempts int, cause error) {
	attempts++
	state := "queued"
	if attempts >= maxRenderAttempts {
		state = "failed"
	}
	delay := time.Duration(1<<attempts) * time.Minute
	_, _ = e.db.Pool().Exec(ctx, `
		UPDATE photo_edits SET render_state = $3, render_error = $4, render_attempts = $5, next_attempt_at = $6
		WHERE photo_id = $1 AND desired_hash = $2`,
		id, hash, state, cause.Error(), attempts, time.Now().Add(delay))
}

func (e *Engine) renderAndPublish(ctx context.Context, id int, rec Recipe, dv int, hash string) error {
	p, err := e.loadPhoto(ctx, id)
	if err != nil {
		return err
	}
	src, err := e.resolveSource(p)
	if err != nil {
		return err
	}
	resolved, err := Resolve(rec, dv)
	if err != nil {
		return err
	}

	e.renderMu.Lock()
	defer e.renderMu.Unlock()

	out, err := e.renderFull(ctx, src, resolved)
	if err != nil {
		return err
	}

	dir := e.thumbs.DevelopDir(id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(dir, ".tmp-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	blur, err := e.writeDerivatives(tmp, out)
	if err != nil {
		return err
	}
	if err := swapDir(tmp, filepath.Join(dir, hash)); err != nil {
		return err
	}
	b := out.Bounds()
	return e.publish(ctx, id, hash, b.Dx(), b.Dy(), blur)
}

func (e *Engine) renderFull(ctx context.Context, src *Source, r Recipe) (*image.NRGBA, error) {
	img, err := e.renderLinear(ctx, src, r)
	if err != nil {
		return nil, err
	}
	return toSRGB8(img), nil
}

func (e *Engine) writeDerivatives(dir string, img *image.NRGBA) (string, error) {
	if err := imaging.Save(img, filepath.Join(dir, "full.jpg"), imaging.JPEGQuality(92)); err != nil {
		return "", err
	}
	var prev image.Image = img
	for _, d := range derivatives {
		t := prev
		if t.Bounds().Dx() > d.width {
			t = imaging.Resize(prev, d.width, 0, imaging.Lanczos)
		}
		if err := imaging.Save(t, filepath.Join(dir, d.name), imaging.JPEGQuality(d.quality)); err != nil {
			return "", err
		}
		prev = t
	}

	blur := blurhashOf(prev)
	b := img.Bounds()
	pw, ph := services.PlaceholderDims(b.Dx(), b.Dy())
	placeholder, err := e.thumbs.GeneratePlaceholder(blur, pw, ph)
	if err != nil {
		return "", err
	}
	f, err := os.Create(filepath.Join(dir, "placeholder.png"))
	if err != nil {
		return "", err
	}
	if err := png.Encode(f, placeholder); err != nil {
		_ = f.Close()
		return "", err
	}
	return blur, f.Close()
}

func swapDir(tmp, final string) error {
	if _, err := os.Stat(final); err == nil {
		old := final + ".old-" + strconv.FormatInt(time.Now().UnixNano(), 36)
		if err := os.Rename(final, old); err != nil {
			return err
		}
		if err := os.Rename(tmp, final); err != nil {
			_ = os.Rename(old, final)
			return err
		}
		return os.RemoveAll(old)
	}
	return os.Rename(tmp, final)
}

func (e *Engine) publish(ctx context.Context, id int, hash string, w, h int, blur string) error {
	tx, err := e.db.Pool().Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		UPDATE photo_edits SET published_hash = $2, render_state = 'idle', render_error = NULL,
			render_attempts = 0, published_at = NOW()
		WHERE photo_id = $1 AND desired_hash = $2`, id, hash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE photos SET edit_hash = $2, width = $3, height = $4, blurhash = $5, updated_at = NOW()
		WHERE id = $1`, id, hash, w, h, blur); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (e *Engine) Rehash(ctx context.Context, force bool) (int, error) {
	type item struct {
		id     int
		recipe []byte
		dv     int
		hash   string
	}
	rows, err := e.db.Pool().Query(ctx, `SELECT photo_id, recipe, defaults_version, desired_hash FROM photo_edits`)
	if err != nil {
		return 0, err
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.recipe, &it.dv, &it.hash); err == nil {
			items = append(items, it)
		}
	}
	rows.Close()

	n := 0
	for _, it := range items {
		p, err := e.loadPhoto(ctx, it.id)
		if err != nil {
			continue
		}
		src, err := e.resolveSource(p)
		if err != nil {
			continue
		}
		var rec Recipe
		if json.Unmarshal(it.recipe, &rec) != nil {
			continue
		}
		resolved, err := Resolve(rec, it.dv)
		if err != nil {
			continue
		}
		hash, err := e.renderHash(ctx, src, resolved)
		if err != nil {
			continue
		}
		switch {
		case hash != it.hash:
			_, err = e.db.Pool().Exec(ctx, `
				UPDATE photo_edits SET desired_hash = $2, render_state = 'queued', render_attempts = 0,
					render_error = NULL, next_attempt_at = NOW(), updated_at = NOW()
				WHERE photo_id = $1`, it.id, hash)
		case force:
			_, err = e.db.Pool().Exec(ctx, `
				UPDATE photo_edits SET published_hash = NULL, render_state = 'queued', render_attempts = 0,
					render_error = NULL, next_attempt_at = NOW()
				WHERE photo_id = $1`, it.id)
		default:
			continue
		}
		if err == nil {
			n++
		}
	}
	if n > 0 {
		e.kick()
	}
	return n, nil
}

func (e *Engine) gc(ctx context.Context) {
	e.full.dropIdle(gcGrace)
	e.exports.gc(exportTTL, filepath.Join(e.cfg.CacheDir, "exports"))
	base := filepath.Join(e.cfg.CacheDir, "develop")
	entries, err := os.ReadDir(base)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-gcGrace)
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		id, err := strconv.Atoi(ent.Name())
		if err != nil {
			continue
		}
		var desired, published string
		err = e.db.Pool().QueryRow(ctx,
			`SELECT desired_hash, COALESCE(published_hash, '') FROM photo_edits WHERE photo_id = $1`, id).
			Scan(&desired, &published)
		active := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		dir := filepath.Join(base, ent.Name())
		subs, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		kept := 0
		for _, s := range subs {
			name := s.Name()
			if active && (name == desired || name == published) {
				kept++
				continue
			}
			info, err := s.Info()
			if err != nil || info.ModTime().After(cutoff) {
				kept++
				continue
			}
			_ = os.RemoveAll(filepath.Join(dir, name))
		}
		if !active && kept == 0 {
			_ = os.Remove(dir)
		}
	}
}

type proxyCache struct {
	mu    sync.Mutex
	max   int
	ll    *list.List
	items map[string]*list.Element
}

type proxyEntry struct {
	key string
	val *loaded
}

func newProxyCache(max int) *proxyCache {
	return &proxyCache{max: max, ll: list.New(), items: map[string]*list.Element{}}
}

func (c *proxyCache) get(key string) (*loaded, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return nil, false
	}
	c.ll.MoveToFront(el)
	return el.Value.(*proxyEntry).val, true
}

func (c *proxyCache) put(key string, v *loaded) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		el.Value.(*proxyEntry).val = v
		c.ll.MoveToFront(el)
		return
	}
	c.items[key] = c.ll.PushFront(&proxyEntry{key: key, val: v})
	for c.ll.Len() > c.max {
		el := c.ll.Back()
		c.ll.Remove(el)
		delete(c.items, el.Value.(*proxyEntry).key)
	}
}

// fullCache keeps the single most recent full-resolution decode for 1:1
// previews; it is dropped before a full render and after gcGrace idle.
type fullCache struct {
	mu   sync.Mutex
	key  string
	val  *loaded
	used time.Time
}

func (c *fullCache) drop() {
	c.mu.Lock()
	c.key, c.val = "", nil
	c.mu.Unlock()
}

func (c *fullCache) dropIdle(idle time.Duration) {
	if !c.mu.TryLock() {
		return
	}
	if c.val != nil && time.Since(c.used) > idle {
		c.key, c.val = "", nil
	}
	c.mu.Unlock()
}

type LensState struct {
	Available  bool            `json:"available"`
	ExifLens   string          `json:"exif_lens,omitempty"`
	Focal      float64         `json:"focal,omitempty"`
	Aperture   float64         `json:"aperture,omitempty"`
	Distance   float64         `json:"distance,omitempty"`
	CameraCrop float64         `json:"camera_crop,omitempty"`
	Confirmed  string          `json:"confirmed,omitempty"`
	Applied    string          `json:"applied,omitempty"`
	Candidates []LensCandidate `json:"candidates"`
	DB         string          `json:"db"`
}

type PreviewRequest struct {
	MaxW    int
	MaxH    int
	Quality string
	Region  *Rect
}

type PreviewResult struct {
	Data    []byte
	Quality string
	FullW   int
	FullH   int
	Region  *Rect
}

type lensChoice struct {
	profile *LensProfile
	exif    *ExifLens
	crop    float64
}

func (e *Engine) lenses() *LensDB {
	e.lensOnce.Do(func() {
		dir := e.cfg.LensfunDir
		if dir == "" {
			dir = detectLensfunDir()
		}
		e.lensDB = loadLensDB(dir)
		log.Printf("develop: lensfun %q: %d lenses, %d cameras", dir, len(e.lensDB.lenses), len(e.lensDB.cameras))
	})
	return e.lensDB
}

func (e *Engine) confirmedLens(ctx context.Context, exifLens string) (string, error) {
	var id string
	err := e.db.Pool().QueryRow(ctx, `SELECT profile FROM lens_profile_map WHERE exif_lens = $1`, exifLens).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, err
}

func (e *Engine) lensFor(ctx context.Context, src *Source, r Recipe) (*lensChoice, error) {
	if !r.Lens.Enabled {
		return nil, nil
	}
	db := e.lenses()
	info := e.dec.LensInfo(ctx, src.Abs, src.Fingerprint)
	id := r.Lens.Profile
	if id == "" && info.Lens != "" {
		var err error
		if id, err = e.confirmedLens(ctx, info.Lens); err != nil {
			return nil, err
		}
	}
	p := db.byID[id]
	if p == nil {
		return nil, nil
	}
	crop := info.CropFactor()
	if crop <= 0 {
		if cam := db.camera(info.Make, info.Model); cam != nil {
			crop = cam.crop
		}
	}
	if crop <= 0 {
		crop = p.crop
	}
	return &lensChoice{profile: p, exif: info, crop: crop}, nil
}

func lensModelFor(ch *lensChoice, srcW, srcH int, r Recipe) *lensModel {
	if ch == nil {
		return nil
	}
	focal := ch.exif.Focal
	if focal <= 0 {
		focal = ch.profile.minFocal
	}
	co := ch.profile.coefficients(focal, ch.exif.Aperture, ch.exif.Distance)
	return newLensModel(ch.profile, co, srcW, srcH, ch.crop, r.Lens)
}

func (e *Engine) renderHash(ctx context.Context, src *Source, resolved Recipe) (string, error) {
	deps := []string{e.dec.LibRawVersion()}
	if resolved.Lens.Enabled {
		id := ""
		if ch, err := e.lensFor(ctx, src, resolved); err != nil {
			return "", err
		} else if ch != nil {
			id = ch.profile.ID
		}
		deps = append(deps, "lens:"+id+"@"+e.lenses().fingerprint)
	}
	return RenderHash(src.Fingerprint, resolved, deps...)
}

func (e *Engine) decodeKey(ctx context.Context, src *Source, r Recipe) (string, error) {
	if src.Kind != "raw" {
		return src.Fingerprint + "|raster", nil
	}
	mul, _, err := e.rawParams(ctx, src, r)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s|%s|%s|%.5f,%.5f,%.5f", src.Fingerprint, r.Raw.Demosaic, r.Raw.Highlights, mul[0], mul[1], mul[2]), nil
}

func (e *Engine) fullDecode(ctx context.Context, src *Source, r Recipe) (*loaded, error) {
	key, err := e.decodeKey(ctx, src, r)
	if err != nil {
		return nil, err
	}
	e.full.mu.Lock()
	defer e.full.mu.Unlock()
	if e.full.key == key && e.full.val != nil {
		e.full.used = time.Now()
		return e.full.val, nil
	}
	e.full.key, e.full.val = "", nil
	l, err := e.load(ctx, src, r, false)
	if err != nil {
		return nil, err
	}
	e.full.key, e.full.val, e.full.used = key, l, time.Now()
	return l, nil
}

func (e *Engine) prepare(ctx context.Context, id int, rec Recipe) (Recipe, *Source, error) {
	rec, err := rec.Normalize()
	if err != nil {
		return Recipe{}, nil, err
	}
	dv, err := e.defaultsVersionFor(ctx, id)
	if err != nil {
		return Recipe{}, nil, err
	}
	resolved, err := Resolve(rec, dv)
	if err != nil {
		return Recipe{}, nil, err
	}
	p, err := e.loadPhoto(ctx, id)
	if err != nil {
		return Recipe{}, nil, err
	}
	src, err := e.resolveSource(p)
	if err != nil {
		return Recipe{}, nil, err
	}
	return resolved, src, nil
}

// wbPlan decides how white balance is applied. quality "fast" lets a RAW
// custom white balance skip the re-decode: the as-shot proxy is corrected
// with a CAT matrix, approximate but instant while dragging.
func (e *Engine) wbPlan(ctx context.Context, src *Source, resolved Recipe, quality string) (Recipe, *Mat3, string, error) {
	decodeRecipe := resolved
	pre := rasterWB(resolved, src.Kind == "raw")
	if src.Kind == "raw" && quality == "fast" && resolved.WB.Mode == WBCustom {
		from, err := e.asShotWhite(ctx, src)
		if err != nil {
			return Recipe{}, nil, "", err
		}
		x, y := TempTintToXY(resolved.WB.Temp, resolved.WB.Tint)
		m := wbMatrix(xyToXYZ(x, y), from)
		decodeRecipe.WB = WBParams{Mode: WBAsShot}
		return decodeRecipe, &m, "fast", nil
	}
	return decodeRecipe, pre, "accurate", nil
}

func (e *Engine) frameFromProxy(l *loaded, r Recipe, lens *lensChoice) (*Image, *geomPlan, int, int) {
	fullW, fullH := newGeomPlan(l.fullW, l.fullH, l.orientation, r.Geometry, nil).size(1)
	plan := newGeomPlan(l.img.W, l.img.H, l.orientation, r.Geometry, lensModelFor(lens, l.img.W, l.img.H, r))
	w, h := plan.size(1)
	return warp(l.img, plan, 1, 0, 0, w, h), plan, fullW, fullH
}

func (e *Engine) acquire(ctx context.Context) (func(), error) {
	select {
	case e.sem <- struct{}{}:
		return func() { <-e.sem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Preview renders the recipe either fitted to MaxW×MaxH from the cached proxy,
// or, with Region set, as a 1:1 tile from the full-resolution decode.
func (e *Engine) Preview(ctx context.Context, id int, rec Recipe, req PreviewRequest) (*PreviewResult, error) {
	resolved, src, err := e.prepare(ctx, id, rec)
	if err != nil {
		return nil, err
	}
	return e.previewSource(ctx, src, resolved, req)
}

func (e *Engine) previewRegion(ctx context.Context, src *Source, resolved, decodeRecipe Recipe, pre *Mat3, quality string, lens *lensChoice, region *Rect) (*PreviewResult, error) {
	tm := timingsFrom(ctx)
	var air *[3]float32
	if resolved.Local.Dehaze != 0 {
		st := time.Now()
		pl, err := e.proxy(ctx, src, decodeRecipe)
		if err != nil {
			return nil, err
		}
		frame, _, _, _ := e.frameFromProxy(pl, resolved, lens)
		applyPre(frame, resolved, pre)
		a := estimateAirlight(frame)
		air = &a
		tm.Since("airlight", st)
	}

	full, err := e.fullDecode(ctx, src, decodeRecipe)
	if err != nil {
		return nil, err
	}
	geo := time.Now()
	plan := newGeomPlan(full.img.W, full.img.H, full.orientation, resolved.Geometry, lensModelFor(lens, full.img.W, full.img.H, resolved))
	fw, fh := plan.size(1)
	rw := clampInt(int(math.Round(region.W*float64(fw))), 16, min(fw, maxRegionSide))
	rh := clampInt(int(math.Round(region.H*float64(fh))), 16, min(fh, maxRegionSide))
	rx := clampInt(int(math.Round(region.X*float64(fw))), 0, fw-rw)
	ry := clampInt(int(math.Round(region.Y*float64(fh))), 0, fh-rh)
	margin := regionMargin(resolved, max(fw, fh))
	x0, y0 := max(0, rx-margin), max(0, ry-margin)
	x1, y1 := min(fw, rx+rw+margin), min(fh, ry+rh+margin)

	tile := warp(full.img, plan, 1, x0, y0, x1-x0, y1-y0)
	tm.Since("geometry", geo)

	applyTone(tile, tile, resolved, pre, renderCtx{
		detailScale: 1,
		frameLong:   float64(max(fw, fh)),
		airlight:    air,
		space:       plan.space(x0, y0, 1, 1),
		timing:      tm,
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	encoded := time.Now()
	data, err := encodeJPEG(toSRGB8(tile.cropPx(rx-x0, ry-y0, rw, rh)), 90)
	tm.Since("encode", encoded)
	if err != nil {
		return nil, err
	}
	return &PreviewResult{
		Data:    data,
		Quality: quality,
		FullW:   fw,
		FullH:   fh,
		Region: &Rect{
			X: float64(rx) / float64(fw), Y: float64(ry) / float64(fh),
			W: float64(rw) / float64(fw), H: float64(rh) / float64(fh),
		},
	}, nil
}

// SampleWB finds the white balance that neutralises the average colour of
// area (normalised to the geometry-applied frame), or of the whole frame when
// area is nil (auto).
func (e *Engine) SampleWB(ctx context.Context, id int, rec Recipe, area *Rect) (*WhiteBalance, error) {
	resolved, src, err := e.prepare(ctx, id, rec)
	if err != nil {
		return nil, err
	}
	return e.sampleSource(ctx, src, resolved, area)
}

func (e *Engine) lensState(ctx context.Context, src *Source, rec Recipe) *LensState {
	db := e.lenses()
	info := e.dec.LensInfo(ctx, src.Abs, src.Fingerprint)
	ls := &LensState{
		Available:  len(db.lenses) > 0,
		ExifLens:   info.Lens,
		Focal:      info.Focal,
		Aperture:   info.Aperture,
		Distance:   info.Distance,
		CameraCrop: info.CropFactor(),
		DB:         db.fingerprint,
	}
	mount := ""
	if cam := db.camera(info.Make, info.Model); cam != nil {
		mount = cam.mount
		if ls.CameraCrop <= 0 {
			ls.CameraCrop = cam.crop
		}
	}
	if info.Lens != "" {
		ls.Confirmed, _ = e.confirmedLens(ctx, info.Lens)
	}
	ls.Candidates = db.Candidates(info.Lens, info.Make, mount, 8)
	have := map[string]bool{}
	for _, c := range ls.Candidates {
		have[c.ID] = true
	}
	for _, id := range []string{ls.Confirmed, rec.Lens.Profile} {
		if p := db.byID[id]; p != nil && !have[id] {
			ls.Candidates = append(ls.Candidates, LensCandidate{ID: p.ID, Maker: p.Maker, Model: p.Model})
			have[id] = true
		}
	}
	if ch, err := e.lensFor(ctx, src, rec); err == nil && ch != nil {
		ls.Applied = ch.profile.ID
	}
	return ls
}

func (e *Engine) LensSearch(q string, limit int) []LensCandidate {
	return e.lenses().Search(q, limit)
}

// SetLensMapping remembers (or, with an empty profile, forgets) which Lensfun
// profile an EXIF lens name means, then requeues edits whose hash changed.
func (e *Engine) SetLensMapping(ctx context.Context, exifLens, profile string) error {
	exifLens = strings.TrimSpace(exifLens)
	if exifLens == "" {
		return fmt.Errorf("%w: lens name is empty", ErrInvalidRecipe)
	}
	var err error
	if profile == "" {
		_, err = e.db.Pool().Exec(ctx, `DELETE FROM lens_profile_map WHERE exif_lens = $1`, exifLens)
	} else {
		if e.lenses().byID[profile] == nil {
			return fmt.Errorf("%w: unknown lens profile", ErrInvalidRecipe)
		}
		_, err = e.db.Pool().Exec(ctx, `
			INSERT INTO lens_profile_map (exif_lens, profile) VALUES ($1, $2)
			ON CONFLICT (exif_lens) DO UPDATE SET profile = EXCLUDED.profile, updated_at = NOW()`, exifLens, profile)
	}
	if err != nil {
		return err
	}
	_, err = e.Rehash(ctx, false)
	return err
}

// sourceFromAbs describes a source file; key names it inside the fingerprint
// (the media-relative path, or staging/<id>/<name> for Unsorted files).
func sourceFromAbs(abs, key string) (*Source, error) {
	fi, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSourceUnavailable, err)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: not a regular file", ErrSourceUnavailable)
	}
	ext := strings.ToLower(filepath.Ext(abs))
	kind := "raster"
	switch ext {
	case ".arw":
		kind = "raw"
	case ".jpg", ".jpeg", ".png":
	default:
		return nil, fmt.Errorf("%w: unsupported format %s", ErrSourceUnavailable, ext)
	}
	return &Source{
		Abs:         abs,
		Ext:         strings.TrimPrefix(ext, "."),
		Kind:        kind,
		Size:        fi.Size(),
		Fingerprint: fmt.Sprintf("%s:%d:%d", key, fi.Size(), fi.ModTime().UnixNano()),
	}, nil
}

func (e *Engine) fillSourceState(ctx context.Context, st *State, src *Source) {
	st.Source = SourceInfo{Available: true, Kind: src.Kind, Format: src.Ext, Size: src.Size}
	st.Capabilities.LensCorrection = len(e.lenses().lenses) > 0
	if src.Kind == "raw" {
		st.Capabilities.RawWhiteBalance = e.dec.Available()
		st.Capabilities.Demosaic = e.dec.Available()
		if !e.dec.Available() {
			st.Source.Available = false
			st.Source.Error = "dcraw_emu is not installed on the server"
			return
		}
	} else if warn := e.dec.RasterProfileWarning(src.Abs); warn != "" {
		st.Warnings = append(st.Warnings, warn)
	}
	if wb, err := e.asShot(ctx, src); err != nil {
		st.Warnings = append(st.Warnings, "as-shot white balance unavailable: "+err.Error())
	} else {
		st.AsShot = wb
	}
	st.Lens = e.lensState(ctx, src, st.Recipe)
}

func (e *Engine) previewSource(ctx context.Context, src *Source, resolved Recipe, req PreviewRequest) (*PreviewResult, error) {
	tm := timingsFrom(ctx)
	queued := time.Now()
	release, err := e.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	tm.Since("queue", queued)

	if req.Region != nil {
		decodeRecipe, pre, quality, err := e.wbPlan(ctx, src, resolved, req.Quality)
		if err != nil {
			return nil, err
		}
		lens, err := e.lensFor(ctx, src, resolved)
		if err != nil {
			return nil, err
		}
		return e.previewRegion(ctx, src, resolved, decodeRecipe, pre, quality, lens, req.Region)
	}

	img, quality, fullW, fullH, err := e.renderProxyFrame(ctx, src, resolved, req.MaxW, req.MaxH, req.Quality)
	if err != nil {
		return nil, err
	}
	encoded := time.Now()
	data, err := encodeJPEG(toSRGB8(img), 85)
	tm.Since("encode", encoded)
	if err != nil {
		return nil, err
	}
	return &PreviewResult{Data: data, Quality: quality, FullW: fullW, FullH: fullH}, nil
}

// renderProxyFrame renders the whole frame from the proxy, fitted to
// maxW×maxH (0 means no limit). The caller holds a preview slot.
func (e *Engine) renderProxyFrame(ctx context.Context, src *Source, resolved Recipe, maxW, maxH int, quality string) (*Image, string, int, int, error) {
	tm := timingsFrom(ctx)
	decodeRecipe, pre, actual, err := e.wbPlan(ctx, src, resolved, quality)
	if err != nil {
		return nil, "", 0, 0, err
	}
	lens, err := e.lensFor(ctx, src, resolved)
	if err != nil {
		return nil, "", 0, 0, err
	}
	l, err := e.proxy(ctx, src, decodeRecipe)
	if err != nil {
		return nil, "", 0, 0, err
	}
	geo := time.Now()
	frame, plan, fullW, fullH := e.frameFromProxy(l, resolved, lens)
	img := frame.FitWithin(maxW, maxH)
	tm.Since("geometry", geo)
	long := float64(max(img.W, img.H))
	applyTone(img, img, resolved, pre, renderCtx{
		detailScale: math.Min(1, long/float64(max(fullW, fullH))),
		frameLong:   long,
		space:       plan.space(0, 0, float64(img.W)/float64(frame.W), float64(img.H)/float64(frame.H)),
		timing:      tm,
	})
	return img, actual, fullW, fullH, ctx.Err()
}

func (e *Engine) sampleSource(ctx context.Context, src *Source, resolved Recipe, area *Rect) (*WhiteBalance, error) {
	lens, err := e.lensFor(ctx, src, resolved)
	if err != nil {
		return nil, err
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
	img, _, _, _ := e.frameFromProxy(l, resolved, lens)

	x0, y0, x1, y1 := 0, 0, img.W, img.H
	if area != nil {
		x0 = clampInt(int(math.Floor(area.X*float64(img.W))), 0, img.W-1)
		y0 = clampInt(int(math.Floor(area.Y*float64(img.H))), 0, img.H-1)
		x1 = clampInt(int(math.Ceil((area.X+area.W)*float64(img.W))), x0+1, img.W)
		y1 = clampInt(int(math.Ceil((area.Y+area.H)*float64(img.H))), y0+1, img.H)
	}
	var sum [3]float64
	n := 0
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			i := (y*img.W + x) * 3
			r, g, b := float64(img.Pix[i]), float64(img.Pix[i+1]), float64(img.Pix[i+2])
			if r >= 0.95 || g >= 0.95 || b >= 0.95 || lumR*r+lumG*g+lumB*b < 0.003 {
				continue
			}
			sum[0] += r
			sum[1] += g
			sum[2] += b
			n++
		}
	}
	if n == 0 {
		return nil, fmt.Errorf("%w: no usable pixels in the sampled area", ErrInvalidRecipe)
	}
	from := whiteD65
	if src.Kind == "raw" {
		if from, err = e.asShotWhite(ctx, src); err != nil {
			return nil, err
		}
	}
	return wbFromSample(sum, from)
}

// renderLinear renders the full-resolution frame in linear Rec.2020; it is
// shared by publishing and export.
func (e *Engine) renderLinear(ctx context.Context, src *Source, r Recipe) (*Image, error) {
	e.full.drop()
	lens, err := e.lensFor(ctx, src, r)
	if err != nil {
		return nil, err
	}
	l, err := e.load(ctx, src, r, false)
	if err != nil {
		return nil, err
	}
	raw := l.raw
	plan := newGeomPlan(l.img.W, l.img.H, l.orientation, r.Geometry, lensModelFor(lens, l.img.W, l.img.H, r))
	w, h := plan.size(1)
	img := warp(l.img, plan, 1, 0, 0, w, h)
	l = nil
	applyTone(img, img, r, rasterWB(r, raw), renderCtx{
		detailScale: 1,
		frameLong:   float64(max(w, h)),
		space:       plan.space(0, 0, 1, 1),
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return img, nil
}

// saveRecipe stores a new revision of a photo's recipe and queues a render
// when the hash changed. expected is the revision the caller edited.
// Interactive saves (the editor) render ahead of batch work, so a long batch
// never leaves the photo someone is editing stuck in the queue.
func (e *Engine) saveRecipe(ctx context.Context, id int, rec Recipe, expected int64, label string, interactive bool) error {
	priority := 0
	if interactive {
		priority = 1
	}
	rec, err := rec.Normalize()
	if err != nil {
		return err
	}
	p, err := e.loadPhoto(ctx, id)
	if err != nil {
		return err
	}
	src, err := e.resolveSource(p)
	if err != nil {
		return err
	}
	if src.Kind == "raw" && !e.dec.Available() {
		return fmt.Errorf("%w: dcraw_emu is not installed", ErrSourceUnavailable)
	}
	recipeJSON, err := json.Marshal(rec)
	if err != nil {
		return err
	}

	tx, err := e.db.Pool().Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var revision int64
	dv := CurrentDefaultsVersion
	err = tx.QueryRow(ctx,
		`SELECT recipe_revision, defaults_version FROM photo_edits WHERE photo_id = $1 FOR UPDATE`, id).
		Scan(&revision, &dv)
	exists := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if revision != expected {
		return ErrConflict
	}

	resolved, err := Resolve(rec, dv)
	if err != nil {
		return err
	}
	hash, err := e.renderHash(ctx, src, resolved)
	if err != nil {
		return err
	}

	if exists {
		_, err = tx.Exec(ctx, `
			UPDATE photo_edits SET recipe = $2, recipe_revision = recipe_revision + 1, desired_hash = $3,
				render_state = CASE WHEN published_hash IS NOT DISTINCT FROM $3 THEN 'idle' ELSE 'queued' END,
				render_error = NULL, render_attempts = 0, next_attempt_at = NOW(), updated_at = NOW(), priority = $4
			WHERE photo_id = $1`, id, recipeJSON, hash, priority)
	} else {
		_, err = tx.Exec(ctx, `
			INSERT INTO photo_edits (photo_id, recipe, defaults_version, recipe_revision, desired_hash,
				source_width, source_height, source_blurhash, legacy_rotation, render_state, priority)
			VALUES ($1, $2, $3, 1, $4, $5, $6, $7, $8, 'queued', $9)`,
			id, recipeJSON, dv, hash, p.Width, p.Height, p.Blurhash, p.Rotation, priority)
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO photo_edit_history (photo_id, revision, recipe, label) VALUES ($1, $2, $3, $4)`,
		id, revision+1, recipeJSON, label); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM photo_edit_history WHERE photo_id = $1 AND id NOT IN (
			SELECT id FROM photo_edit_history WHERE photo_id = $1 ORDER BY id DESC LIMIT $2)`,
		id, historyLimit); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

const warmTimeout = 3 * time.Minute

// Warm decodes the full-resolution image in the background while the user
// looks at the fit preview, so the first 1:1 view does not wait for it.
func (e *Engine) Warm(id int, rec Recipe) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), warmTimeout)
		defer cancel()
		resolved, src, err := e.prepare(ctx, id, rec)
		if err == nil {
			e.warmSource(ctx, src, resolved)
		}
	}()
}

func (e *Engine) WarmStaged(sid string, rec Recipe) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), warmTimeout)
		defer cancel()
		resolved, src, err := e.prepareStaged(ctx, sid, rec)
		if err == nil {
			e.warmSource(ctx, src, resolved)
		}
	}()
}

// warmSource skips warming while a publish or export holds renderMu, so two
// full-resolution decodes never compete for memory.
func (e *Engine) warmSource(ctx context.Context, src *Source, resolved Recipe) {
	if !e.renderMu.TryLock() {
		return
	}
	e.renderMu.Unlock()
	decodeRecipe, _, _, err := e.wbPlan(ctx, src, resolved, "accurate")
	if err != nil {
		return
	}
	started := time.Now()
	if _, err := e.fullDecode(ctx, src, decodeRecipe); err != nil {
		log.Printf("develop: warm %s: %v", filepath.Base(src.Abs), err)
		return
	}
	if d := time.Since(started); d > time.Second {
		log.Printf("develop: warmed full decode of %s in %s", filepath.Base(src.Abs), d.Round(time.Millisecond))
	}
}
