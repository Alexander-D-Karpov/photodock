package develop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"path/filepath"
	"regexp"
	"time"

	"github.com/disintegration/imaging"
	"github.com/jackc/pgx/v5"
)

// Editing Unsorted (staging) files: recipe lives in staged_edits, drives staging previews, and is adopted by the photo on commit.

const (
	stagingViewQuality    = 92
	stagingPreviewQuality = 82
	stagingPreviewW       = 400
	stagingRenderTimeout  = 2 * time.Minute
	stagedAdoptLabel      = "Edited in Unsorted"
)

var stagedIDPattern = regexp.MustCompile(`^[0-9]+-[0-9a-f]+$`)

type stagedRow struct {
	Filename string
	Rotation int
	Recipe   *Recipe
	Revision int64
}

func (e *Engine) loadStaged(ctx context.Context, sid string) (*stagedRow, error) {
	if !stagedIDPattern.MatchString(sid) {
		return nil, ErrNotFound
	}
	var row stagedRow
	var raw []byte
	var rev *int64
	err := e.db.Pool().QueryRow(ctx, `
		SELECT su.filename, COALESCE(su.rotation, 0), se.recipe, se.revision
		FROM staged_uploads su LEFT JOIN staged_edits se ON se.staged_id = su.id
		WHERE su.id = $1`, sid).Scan(&row.Filename, &row.Rotation, &raw, &rev)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if raw != nil && rev != nil {
		var r Recipe
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, err
		}
		row.Recipe = &r
		row.Revision = *rev
	}
	return &row, nil
}

func (e *Engine) resolveStagedSource(sid, filename string) (*Source, error) {
	abs := filepath.Join(e.cfg.CacheDir, "staging", sid, filepath.Base(filename))
	return sourceFromAbs(abs, "staging/"+sid+"/"+filepath.Base(filename))
}

func (e *Engine) prepareStaged(ctx context.Context, sid string, rec Recipe) (Recipe, *Source, error) {
	rec, err := rec.Normalize()
	if err != nil {
		return Recipe{}, nil, err
	}
	resolved, err := Resolve(rec, CurrentDefaultsVersion)
	if err != nil {
		return Recipe{}, nil, err
	}
	row, err := e.loadStaged(ctx, sid)
	if err != nil {
		return Recipe{}, nil, err
	}
	src, err := e.resolveStagedSource(sid, row.Filename)
	if err != nil {
		return Recipe{}, nil, err
	}
	return resolved, src, nil
}

func (e *Engine) StagedState(ctx context.Context, sid string) (*State, error) {
	row, err := e.loadStaged(ctx, sid)
	if err != nil {
		return nil, err
	}
	defs, err := Defaults(CurrentDefaultsVersion)
	if err != nil {
		return nil, err
	}
	st := &State{StagedID: sid, Defaults: defs, DefaultsVersion: CurrentDefaultsVersion, RenderState: "none"}
	if row.Recipe != nil {
		st.Active = true
		st.Recipe = *row.Recipe
		st.Revision = row.Revision
	} else {
		st.Recipe = Recipe{Geometry: Geometry{Rotate: NormalizeRotation(row.Rotation)}}
	}
	src, err := e.resolveStagedSource(sid, row.Filename)
	if err != nil {
		st.Source = SourceInfo{Available: false, Error: err.Error()}
		return st, nil
	}
	e.fillSourceState(ctx, st, src)
	return st, nil
}

func (e *Engine) SaveStaged(ctx context.Context, sid string, rec Recipe, expected int64) (*State, error) {
	rec, err := rec.Normalize()
	if err != nil {
		return nil, err
	}
	row, err := e.loadStaged(ctx, sid)
	if err != nil {
		return nil, err
	}
	if _, err := e.resolveStagedSource(sid, row.Filename); err != nil {
		return nil, err
	}
	current := int64(0)
	if row.Recipe != nil {
		current = row.Revision
	}
	if current != expected {
		return nil, ErrConflict
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return nil, err
	}
	tag, err := e.db.Pool().Exec(ctx, `
		INSERT INTO staged_edits (staged_id, recipe, revision) VALUES ($1, $2, 1)
		ON CONFLICT (staged_id) DO UPDATE SET recipe = EXCLUDED.recipe,
			revision = staged_edits.revision + 1, updated_at = NOW()
		WHERE staged_edits.revision = $3`, sid, raw, expected)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrConflict
	}
	e.scheduleStagingPreview(sid)
	return e.StagedState(ctx, sid)
}

func (e *Engine) ResetStaged(ctx context.Context, sid string) (*State, error) {
	if !stagedIDPattern.MatchString(sid) {
		return nil, ErrNotFound
	}
	tag, err := e.db.Pool().Exec(ctx, `DELETE FROM staged_edits WHERE staged_id = $1`, sid)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() > 0 {
		e.scheduleStagingPreview(sid)
	}
	return e.StagedState(ctx, sid)
}

func (e *Engine) PreviewStaged(ctx context.Context, sid string, rec Recipe, req PreviewRequest) (*PreviewResult, error) {
	resolved, src, err := e.prepareStaged(ctx, sid, rec)
	if err != nil {
		return nil, err
	}
	return e.previewSource(ctx, src, resolved, req)
}

func (e *Engine) SampleWBStaged(ctx context.Context, sid string, rec Recipe, area *Rect) (*WhiteBalance, error) {
	resolved, src, err := e.prepareStaged(ctx, sid, rec)
	if err != nil {
		return nil, err
	}
	return e.sampleSource(ctx, src, resolved, area)
}

// RotateStaged rotates an edited staged file via its recipe (crop, masks); false if no recipe, so caller uses legacy rotation.
func (e *Engine) RotateStaged(ctx context.Context, sid string, delta int) (bool, error) {
	row, err := e.loadStaged(ctx, sid)
	if err != nil {
		return false, err
	}
	if row.Recipe == nil {
		return false, nil
	}
	rec, err := RotateRecipe(*row.Recipe, delta).Normalize()
	if err != nil {
		return true, err
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return true, err
	}
	if _, err := e.db.Pool().Exec(ctx, `
		UPDATE staged_edits SET recipe = $2, revision = revision + 1, updated_at = NOW()
		WHERE staged_id = $1`, sid, raw); err != nil {
		return true, err
	}
	return true, e.RenderStagingPreviews(ctx, sid)
}

func (e *Engine) scheduleStagingPreview(sid string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), stagingRenderTimeout)
		defer cancel()
		if err := e.RenderStagingPreviews(ctx, sid); err != nil && !errors.Is(err, ErrNotFound) {
			log.Printf("develop: staging preview %s: %v", sid, err)
		}
	}()
}

// RenderStagingPreviews rewrites staging preview.jpg/view.jpg from the recipe (or legacy rotation) and bumps edit_rev for refetch.
func (e *Engine) RenderStagingPreviews(ctx context.Context, sid string) error {
	lock := e.dec.keyLock("staging:" + sid)
	lock.Lock()
	defer lock.Unlock()

	row, err := e.loadStaged(ctx, sid)
	if err != nil {
		return err
	}
	src, err := e.resolveStagedSource(sid, row.Filename)
	if err != nil {
		return err
	}
	dir := filepath.Join(e.cfg.CacheDir, "staging", sid)

	var w, h int
	if row.Recipe == nil {
		if w, h, err = e.thumbs.MakeStagingPreviews(src.Abs, dir, row.Rotation); err != nil {
			return err
		}
	} else {
		resolved, err := Resolve(*row.Recipe, CurrentDefaultsVersion)
		if err != nil {
			return err
		}
		release, err := e.acquire(ctx)
		if err != nil {
			return err
		}
		img, _, fw, fh, err := e.renderProxyFrame(ctx, src, resolved, 0, 0, "accurate")
		release()
		if err != nil {
			return err
		}
		out := toSRGB8(img)
		if err := imaging.Save(out, filepath.Join(dir, "view.jpg"), imaging.JPEGQuality(stagingViewQuality)); err != nil {
			return err
		}
		small := imaging.Resize(out, min(stagingPreviewW, out.Bounds().Dx()), 0, imaging.Lanczos)
		if err := imaging.Save(small, filepath.Join(dir, "preview.jpg"), imaging.JPEGQuality(stagingPreviewQuality)); err != nil {
			return err
		}
		w, h = fw, fh
	}
	_, err = e.db.Pool().Exec(ctx,
		`UPDATE staged_uploads SET width = $2, height = $3, edit_rev = edit_rev + 1 WHERE id = $1`, sid, w, h)
	return err
}

// AdoptStaged moves a staged recipe onto the photo created from it on commit.
func (e *Engine) AdoptStaged(ctx context.Context, sid string, photoID int) error {
	row, err := e.loadStaged(ctx, sid)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if row.Recipe == nil {
		return nil
	}
	if err := e.saveRecipe(ctx, photoID, *row.Recipe, 0, stagedAdoptLabel, true); err != nil {
		return fmt.Errorf("adopt staged %s into photo %d: %w", sid, photoID, err)
	}
	e.kick()
	return nil
}

func rotateNormPoint(x, y float64, dir int) (float64, float64) {
	if dir > 0 {
		return 1 - y, x
	}
	return y, 1 - x
}

// RotateRecipe turns the result by dir (±90) degrees, carrying crop and masks; mirrors rotate() in the editor's main.js.
func RotateRecipe(r Recipe, dir int) Recipe {
	g := &r.Geometry
	step := dir
	if g.FlipH != g.FlipV {
		step = -dir
	}
	g.Rotate = NormalizeRotation(g.Rotate + step)
	if c := g.Crop; c != nil {
		if dir > 0 {
			g.Crop = &Rect{X: 1 - c.Y - c.H, Y: c.X, W: c.H, H: c.W}
		} else {
			g.Crop = &Rect{X: c.Y, Y: 1 - c.X - c.W, W: c.H, H: c.W}
		}
	}
	r.Masks = append([]Mask(nil), r.Masks...)
	for i := range r.Masks {
		m := &r.Masks[i]
		if m.Type == "linear" {
			m.X0, m.Y0 = rotateNormPoint(m.X0, m.Y0, dir)
			m.X1, m.Y1 = rotateNormPoint(m.X1, m.Y1, dir)
			continue
		}
		m.CX, m.CY = rotateNormPoint(m.CX, m.CY, dir)
		if dir > 0 {
			m.Angle += 90
		} else {
			m.Angle -= 90
		}
		m.Angle = math.Mod(m.Angle+540, 360) - 180
	}
	return r
}
