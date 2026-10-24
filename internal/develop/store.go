package develop

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type photoRow struct {
	ID       int
	Path     string
	Filename string
	Width    int
	Height   int
	Blurhash string
	Rotation int
	EditHash string
}

type editRow struct {
	Recipe          Recipe
	DefaultsVersion int
	Revision        int64
	DesiredHash     string
	PublishedHash   string
	RenderState     string
	RenderError     string
	Attempts        int
	UpdatedAt       time.Time
}

type HistoryEntry struct {
	ID        int64     `json:"id"`
	Revision  int64     `json:"revision"`
	Recipe    Recipe    `json:"recipe"`
	Label     string    `json:"label"`
	CreatedAt time.Time `json:"created_at"`
}

func (e *Engine) loadPhoto(ctx context.Context, id int) (*photoRow, error) {
	var p photoRow
	err := e.db.Pool().QueryRow(ctx, `
		SELECT id, path, filename, COALESCE(width, 0), COALESCE(height, 0), COALESCE(blurhash, ''),
		       COALESCE(rotation, 0), COALESCE(edit_hash, '')
		FROM photos WHERE id = $1`, id).
		Scan(&p.ID, &p.Path, &p.Filename, &p.Width, &p.Height, &p.Blurhash, &p.Rotation, &p.EditHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (e *Engine) loadEdit(ctx context.Context, id int) (*editRow, error) {
	var r editRow
	var raw []byte
	err := e.db.Pool().QueryRow(ctx, `
		SELECT recipe, defaults_version, recipe_revision, desired_hash, COALESCE(published_hash, ''),
		       render_state, COALESCE(render_error, ''), render_attempts, updated_at
		FROM photo_edits WHERE photo_id = $1`, id).
		Scan(&raw, &r.DefaultsVersion, &r.Revision, &r.DesiredHash, &r.PublishedHash,
			&r.RenderState, &r.RenderError, &r.Attempts, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &r.Recipe); err != nil {
		return nil, err
	}
	return &r, nil
}

func (e *Engine) History(ctx context.Context, id int) ([]HistoryEntry, error) {
	rows, err := e.db.Pool().Query(ctx, `
		SELECT id, revision, recipe, label, created_at FROM photo_edit_history
		WHERE photo_id = $1 ORDER BY id DESC LIMIT $2`, id, historyLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HistoryEntry{}
	for rows.Next() {
		var h HistoryEntry
		var raw []byte
		if err := rows.Scan(&h.ID, &h.Revision, &raw, &h.Label, &h.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &h.Recipe)
		out = append(out, h)
	}
	return out, rows.Err()
}
