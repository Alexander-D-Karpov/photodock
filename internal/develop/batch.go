package develop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const batchLimit = 500

type Preset struct {
	ID        int       `json:"id"`
	Name      string    `json:"name"`
	Group     string    `json:"group,omitempty"`
	Builtin   bool      `json:"builtin,omitempty"`
	Recipe    Recipe    `json:"recipe"`
	Modules   []string  `json:"modules"`
	CreatedAt time.Time `json:"created_at"`
}

type BatchError struct {
	PhotoID int    `json:"photo_id"`
	Error   string `json:"error"`
}

type BatchResult struct {
	Applied   int          `json:"applied"`
	Unchanged int          `json:"unchanged"`
	Failed    []BatchError `json:"failed"`
}

func (e *Engine) ListPresets(ctx context.Context) ([]Preset, error) {
	out := append([]Preset{}, BuiltinPresets()...)
	rows, err := e.db.Pool().Query(ctx, `SELECT id, name, recipe, modules, created_at FROM develop_presets ORDER BY lower(name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p Preset
		var raw []byte
		if err := rows.Scan(&p.ID, &p.Name, &raw, &p.Modules, &p.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &p.Recipe)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (e *Engine) SavePreset(ctx context.Context, name string, rec Recipe, modules []string) (*Preset, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return nil, fmt.Errorf("%w: preset name must be 1-100 characters", ErrInvalidRecipe)
	}
	rec, err := rec.Normalize()
	if err != nil {
		return nil, err
	}
	picked, err := PickModules(rec, modules)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(picked)
	if err != nil {
		return nil, err
	}
	p := &Preset{Name: name, Recipe: picked, Modules: modules}
	err = e.db.Pool().QueryRow(ctx, `
		INSERT INTO develop_presets (name, recipe, modules) VALUES ($1, $2, $3)
		ON CONFLICT (name) DO UPDATE SET recipe = EXCLUDED.recipe, modules = EXCLUDED.modules
		RETURNING id, created_at`, name, raw, modules).Scan(&p.ID, &p.CreatedAt)
	return p, err
}

func (e *Engine) DeletePreset(ctx context.Context, id int) error {
	if id <= 0 {
		return fmt.Errorf("%w: built-in presets cannot be deleted", ErrInvalidRecipe)
	}
	_, err := e.db.Pool().Exec(ctx, `DELETE FROM develop_presets WHERE id = $1`, id)
	return err
}

func (e *Engine) currentRecipe(ctx context.Context, id int) (Recipe, int64, error) {
	row, err := e.loadEdit(ctx, id)
	if err != nil {
		return Recipe{}, 0, err
	}
	if row != nil {
		return row.Recipe, row.Revision, nil
	}
	p, err := e.loadPhoto(ctx, id)
	if err != nil {
		return Recipe{}, 0, err
	}
	return Recipe{Geometry: Geometry{Rotate: NormalizeRotation(p.Rotation)}}, 0, nil
}

func checkBatch(ids []int) error {
	if len(ids) == 0 || len(ids) > batchLimit {
		return fmt.Errorf("%w: select 1-%d photos", ErrInvalidRecipe, batchLimit)
	}
	return nil
}

// ApplyBatch merges the given modules of patch into each photo's recipe.
func (e *Engine) ApplyBatch(ctx context.Context, ids []int, patch Recipe, modules []string, label string) (*BatchResult, error) {
	if err := checkBatch(ids); err != nil {
		return nil, err
	}
	if err := ValidModules(modules); err != nil {
		return nil, err
	}
	patch, err := patch.Normalize()
	if err != nil {
		return nil, err
	}
	if label == "" {
		label = "Batch"
	}
	res := &BatchResult{Failed: []BatchError{}}
	for _, id := range ids {
		cur, rev, err := e.currentRecipe(ctx, id)
		if err == nil {
			var merged Recipe
			if merged, err = MergeModules(cur, patch, modules); err == nil {
				if sameRecipe(cur, merged) {
					res.Unchanged++
					continue
				}
				err = e.saveRecipe(ctx, id, merged, rev, label, false)
			}
		}
		if err != nil {
			res.Failed = append(res.Failed, BatchError{PhotoID: id, Error: err.Error()})
			continue
		}
		res.Applied++
	}
	e.kick()
	return res, nil
}

func (e *Engine) ResetBatch(ctx context.Context, ids []int) (*BatchResult, error) {
	if err := checkBatch(ids); err != nil {
		return nil, err
	}
	res := &BatchResult{Failed: []BatchError{}}
	for _, id := range ids {
		if err := e.Reset(ctx, id); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			res.Failed = append(res.Failed, BatchError{PhotoID: id, Error: err.Error()})
			continue
		}
		res.Applied++
	}
	return res, nil
}
