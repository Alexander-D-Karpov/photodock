package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Alexander-D-Karpov/photodock/internal/develop"
)

const (
	developBodyLimit = 256 << 10
	slowPreview      = 2 * time.Second
)

func (h *Handlers) registerDevelopRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/develop/{id}", h.adminAuth(h.adminDevelopPage))
	mux.HandleFunc("GET /admin/api/develop/{id}", h.adminAuth(h.developState))
	mux.HandleFunc("PUT /admin/api/develop/{id}", h.adminAuth(h.developSave))
	mux.HandleFunc("DELETE /admin/api/develop/{id}", h.adminAuth(h.developReset))
	mux.HandleFunc("POST /admin/api/develop/{id}/preview", h.adminAuth(h.developPreview))
	mux.HandleFunc("POST /admin/api/develop/{id}/wb-sample", h.adminAuth(h.developWBSample))
	mux.HandleFunc("GET /admin/api/develop/{id}/history", h.adminAuth(h.developHistory))
	mux.HandleFunc("POST /admin/api/develop/requeue", h.adminAuth(h.developRequeue))
	mux.HandleFunc("PUT /admin/api/lens-map", h.adminAuth(h.developLensMap))
	mux.HandleFunc("GET /admin/api/lens/search", h.adminAuth(h.developLensSearch))
	h.registerDevelopBatchRoutes(mux)
	h.registerStagedDevelopRoutes(mux)
	h.registerDevelopAutoRoutes(mux)
}

// versionedURL appends ?v=<edit hash> so a re-render gets a fresh URL that can
// be cached immutably; unedited photos keep their bare legacy URL.
func versionedURL(base, hash string) string {
	if hash == "" {
		return base
	}
	return base + "?v=" + hash
}

func (h *Handlers) publishedFile(id int, hash, name string) (string, bool) {
	if hash == "" || h.develop == nil {
		return "", false
	}
	return h.develop.PublishedFile(id, hash, name)
}

// setDerivedCache caches a published render immutably only when the request
// names that exact render; an unversioned URL may change on the next edit.
func setDerivedCache(w http.ResponseWriter, r *http.Request, hash string) {
	if r.URL.Query().Get("v") == hash {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=60")
}

// setLegacyCache is for the pre-develop derivatives. An edited photo whose
// render is not on disk yet falls back to them, and that must not be cached.
func setLegacyCache(w http.ResponseWriter, editHash string) {
	if editHash != "" {
		w.Header().Set("Cache-Control", "no-store")
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
}

func pathID(w http.ResponseWriter, r *http.Request) (int, bool) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id <= 0 {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

func queryInt(r *http.Request, key string, def, lo, hi int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil {
		return def
	}
	return min(max(v, lo), hi)
}

func (h *Handlers) developError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, develop.ErrNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, develop.ErrInvalidRecipe):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, develop.ErrConflict):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, develop.ErrSourceUnavailable):
		http.Error(w, err.Error(), http.StatusFailedDependency)
	default:
		log.Printf("develop: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

type developStripItem struct {
	ID     interface{} `json:"id"`
	Name   string      `json:"name"`
	Hash   string      `json:"hash"`
	Hidden bool        `json:"hidden"`
	Edited bool        `json:"edited"`
	Href   string      `json:"href"`
	Thumb  string      `json:"thumb"`
}

func (h *Handlers) adminDevelopPage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	var folderID sql.NullInt64
	var filename string
	if err := h.db.Pool().QueryRow(ctx, "SELECT folder_id, filename FROM photos WHERE id = $1", id).
		Scan(&folderID, &filename); err != nil {
		http.NotFound(w, r)
		return
	}

	strip := []developStripItem{}
	rows, err := h.db.Pool().Query(ctx, `
		SELECT p.id, p.filename, COALESCE(p.edit_hash, ''), p.hidden, (e.photo_id IS NOT NULL)
		FROM photos p LEFT JOIN photo_edits e ON e.photo_id = p.id
		WHERE p.folder_id IS NOT DISTINCT FROM $1
		ORDER BY COALESCE(p.taken_at, p.created_at) DESC, p.id DESC LIMIT 2000`, folderID)
	if err == nil {
		for rows.Next() {
			var pid int
			var it developStripItem
			if rows.Scan(&pid, &it.Name, &it.Hash, &it.Hidden, &it.Edited) == nil {
				it.ID = pid
				it.Href = fmt.Sprintf("/admin/develop/%d", pid)
				it.Thumb = versionedURL(fmt.Sprintf("/thumb/small/%d", pid), it.Hash)
				strip = append(strip, it)
			}
		}
		rows.Close()
	}

	w.Header().Set("Cache-Control", "no-store")
	h.render(w, "admin/develop.html", map[string]interface{}{
		"Title": "Develop " + filename,
		"Config": map[string]interface{}{
			"id":       id,
			"filename": filename,
			"strip":    strip,
			"back":     fmt.Sprintf("/admin/photos/%d", id),
		},
	})
}

func (h *Handlers) developState(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	st, err := h.develop.State(r.Context(), id)
	if err != nil {
		h.developError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	h.jsonResponse(w, st)
}

func (h *Handlers) readSave(w http.ResponseWriter, r *http.Request) (develop.Recipe, int64, string, bool) {
	var req struct {
		Recipe   json.RawMessage `json:"recipe"`
		Revision int64           `json:"revision"`
		Label    string          `json:"label"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, developBodyLimit)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return develop.Recipe{}, 0, "", false
	}
	rec, err := develop.ParseRecipe(req.Recipe)
	if err != nil {
		h.developError(w, err)
		return develop.Recipe{}, 0, "", false
	}
	if len(req.Label) > 200 {
		req.Label = req.Label[:200]
	}
	return rec, req.Revision, req.Label, true
}

// writeSaveResult answers a conflict with 409 and the current state so the
// editor can offer "load latest" or "keep mine".
func (h *Handlers) writeSaveResult(w http.ResponseWriter, st *develop.State, err error, current func() (*develop.State, error)) {
	if errors.Is(err, develop.ErrConflict) {
		if cur, cerr := current(); cerr == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": "conflict", "state": cur})
			return
		}
	}
	if err != nil {
		h.developError(w, err)
		return
	}
	h.jsonResponse(w, st)
}

func (h *Handlers) developSave(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	rec, rev, label, ok := h.readSave(w, r)
	if !ok {
		return
	}
	st, err := h.develop.Save(r.Context(), id, rec, rev, label)
	h.writeSaveResult(w, st, err, func() (*develop.State, error) { return h.develop.State(r.Context(), id) })
}

func (h *Handlers) developReset(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := h.develop.Reset(r.Context(), id); err != nil {
		h.developError(w, err)
		return
	}
	st, err := h.develop.State(r.Context(), id)
	if err != nil {
		h.developError(w, err)
		return
	}
	h.jsonResponse(w, st)
}

func parseRegion(s string) (*develop.Rect, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return nil, fmt.Errorf("%w: region must be x,y,w,h", develop.ErrInvalidRecipe)
	}
	var v [4]float64
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil || math.IsNaN(f) || f < 0 || f > 1 {
			return nil, fmt.Errorf("%w: bad region value %q", develop.ErrInvalidRecipe, p)
		}
		v[i] = f
	}
	if v[2] <= 0 || v[3] <= 0 {
		return nil, fmt.Errorf("%w: empty region", develop.ErrInvalidRecipe)
	}
	return &develop.Rect{X: v[0], Y: v[1], W: v[2], H: v[3]}, nil
}

func (h *Handlers) readPreviewRequest(w http.ResponseWriter, r *http.Request) (develop.Recipe, develop.PreviewRequest, int64, bool) {
	var req struct {
		Recipe     json.RawMessage `json:"recipe"`
		Generation int64           `json:"generation"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, developBodyLimit)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return develop.Recipe{}, develop.PreviewRequest{}, 0, false
	}
	rec, err := develop.ParseRecipe(req.Recipe)
	if err != nil {
		h.developError(w, err)
		return develop.Recipe{}, develop.PreviewRequest{}, 0, false
	}
	preq := develop.PreviewRequest{
		MaxW:    queryInt(r, "w", 1600, 64, 4096),
		MaxH:    queryInt(r, "h", 1200, 64, 4096),
		Quality: r.URL.Query().Get("quality"),
	}
	if s := r.URL.Query().Get("region"); s != "" {
		if preq.Region, err = parseRegion(s); err != nil {
			h.developError(w, err)
			return develop.Recipe{}, develop.PreviewRequest{}, 0, false
		}
	}
	return rec, preq, req.Generation, true
}

func (h *Handlers) writePreview(w http.ResponseWriter, r *http.Request, res *develop.PreviewResult, gen int64, tm *develop.Timings, err error) {
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		h.developError(w, err)
		return
	}
	hd := w.Header()
	hd.Set("Content-Type", "image/jpeg")
	hd.Set("Cache-Control", "no-store")
	hd.Set("X-Generation", strconv.FormatInt(gen, 10))
	hd.Set("X-Quality", res.Quality)
	hd.Set("X-Full-Width", strconv.Itoa(res.FullW))
	hd.Set("X-Full-Height", strconv.Itoa(res.FullH))
	if res.Region != nil {
		hd.Set("X-Region", fmt.Sprintf("%.6f,%.6f,%.6f,%.6f", res.Region.X, res.Region.Y, res.Region.W, res.Region.H))
	}
	if st := tm.Header(); st != "" {
		hd.Set("Server-Timing", st)
	}
	if tm.Total() > slowPreview {
		log.Printf("develop: slow preview %s: %s", r.URL.Path, tm)
	}
	_, _ = w.Write(res.Data)
}

func (h *Handlers) developPreview(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	rec, preq, gen, ok := h.readPreviewRequest(w, r)
	if !ok {
		return
	}
	ctx, tm := develop.WithTimings(r.Context())
	res, err := h.develop.Preview(ctx, id, rec, preq)
	h.writePreview(w, r, res, gen, tm, err)
}

func (h *Handlers) readWBSample(w http.ResponseWriter, r *http.Request) (develop.Recipe, *develop.Rect, bool) {
	var req struct {
		Recipe json.RawMessage `json:"recipe"`
		X      float64         `json:"x"`
		Y      float64         `json:"y"`
		R      float64         `json:"r"`
		Auto   bool            `json:"auto"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, developBodyLimit)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return develop.Recipe{}, nil, false
	}
	rec, err := develop.ParseRecipe(req.Recipe)
	if err != nil {
		h.developError(w, err)
		return develop.Recipe{}, nil, false
	}
	if req.Auto {
		return rec, nil, true
	}
	rad := req.R
	if rad <= 0 || rad > 0.2 {
		rad = 0.01
	}
	return rec, &develop.Rect{X: req.X - rad, Y: req.Y - rad, W: 2 * rad, H: 2 * rad}, true
}

func (h *Handlers) developWBSample(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	rec, area, ok := h.readWBSample(w, r)
	if !ok {
		return
	}
	wb, err := h.develop.SampleWB(r.Context(), id, rec, area)
	if err != nil {
		h.developError(w, err)
		return
	}
	h.jsonResponse(w, wb)
}

func (h *Handlers) developHistory(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	items, err := h.develop.History(r.Context(), id)
	if err != nil {
		h.developError(w, err)
		return
	}
	h.jsonResponse(w, map[string]interface{}{"items": items})
}

func (h *Handlers) developRequeue(w http.ResponseWriter, r *http.Request) {
	n, err := h.develop.Rehash(r.Context(), r.URL.Query().Get("force") == "1")
	if err != nil {
		h.developError(w, err)
		return
	}
	h.jsonResponse(w, map[string]interface{}{"queued": n})
}

func (h *Handlers) developLensMap(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ExifLens string `json:"exif_lens"`
		Profile  string `json:"profile"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, developBodyLimit)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.develop.SetLensMapping(r.Context(), req.ExifLens, req.Profile); err != nil {
		h.developError(w, err)
		return
	}
	h.jsonResponse(w, map[string]interface{}{"ok": true})
}

func (h *Handlers) developLensSearch(w http.ResponseWriter, r *http.Request) {
	h.jsonResponse(w, map[string]interface{}{"items": h.develop.LensSearch(r.URL.Query().Get("q"), 30)})
}
