package handlers

import (
	"net/http"

	"github.com/Alexander-D-Karpov/photodock/internal/develop"
)

// Editing files still in Unsorted. The API lives under its own prefix so the
// Go 1.22 mux patterns never overlap /admin/api/develop/{id}.
func (h *Handlers) registerStagedDevelopRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/develop/staged/{sid}", h.adminAuth(h.adminDevelopStagedPage))
	mux.HandleFunc("GET /admin/api/staged-develop/{sid}", h.adminAuth(h.stagedDevelopState))
	mux.HandleFunc("PUT /admin/api/staged-develop/{sid}", h.adminAuth(h.stagedDevelopSave))
	mux.HandleFunc("DELETE /admin/api/staged-develop/{sid}", h.adminAuth(h.stagedDevelopReset))
	mux.HandleFunc("POST /admin/api/staged-develop/{sid}/preview", h.adminAuth(h.stagedDevelopPreview))
	mux.HandleFunc("POST /admin/api/staged-develop/{sid}/wb-sample", h.adminAuth(h.stagedDevelopWBSample))
	mux.HandleFunc("GET /admin/api/staged-develop/{sid}/history", h.adminAuth(h.stagedDevelopHistory))
}

func (h *Handlers) adminDevelopStagedPage(w http.ResponseWriter, r *http.Request) {
	sid := r.PathValue("sid")
	ctx := r.Context()
	var filename string
	if err := h.db.Pool().QueryRow(ctx, "SELECT filename FROM staged_uploads WHERE id = $1", sid).Scan(&filename); err != nil {
		http.NotFound(w, r)
		return
	}
	strip := []developStripItem{}
	if items, err := h.loadStagedItems(ctx); err == nil {
		for _, it := range items {
			strip = append(strip, developStripItem{
				ID:     it.ID,
				Name:   it.Filename,
				Edited: it.Edited,
				Href:   "/admin/develop/staged/" + it.ID,
				Thumb:  it.PreviewURL,
			})
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	h.render(w, "admin/develop.html", map[string]interface{}{
		"Title": "Develop " + filename + " (Unsorted)",
		"Config": map[string]interface{}{
			"staged":   sid,
			"filename": filename,
			"strip":    strip,
			"back":     "/admin",
		},
	})
}

func (h *Handlers) stagedDevelopState(w http.ResponseWriter, r *http.Request) {
	st, err := h.develop.StagedState(r.Context(), r.PathValue("sid"))
	if err != nil {
		h.developError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	h.jsonResponse(w, st)
}

func (h *Handlers) stagedDevelopSave(w http.ResponseWriter, r *http.Request) {
	sid := r.PathValue("sid")
	rec, rev, _, ok := h.readSave(w, r)
	if !ok {
		return
	}
	st, err := h.develop.SaveStaged(r.Context(), sid, rec, rev)
	h.writeSaveResult(w, st, err, func() (*develop.State, error) { return h.develop.StagedState(r.Context(), sid) })
}

func (h *Handlers) stagedDevelopReset(w http.ResponseWriter, r *http.Request) {
	st, err := h.develop.ResetStaged(r.Context(), r.PathValue("sid"))
	if err != nil {
		h.developError(w, err)
		return
	}
	h.jsonResponse(w, st)
}

func (h *Handlers) stagedDevelopPreview(w http.ResponseWriter, r *http.Request) {
	rec, preq, gen, ok := h.readPreviewRequest(w, r)
	if !ok {
		return
	}
	ctx, tm := develop.WithTimings(r.Context())
	res, err := h.develop.PreviewStaged(ctx, r.PathValue("sid"), rec, preq)
	h.writePreview(w, r, res, gen, tm, err)
}

func (h *Handlers) stagedDevelopWBSample(w http.ResponseWriter, r *http.Request) {
	rec, area, ok := h.readWBSample(w, r)
	if !ok {
		return
	}
	wb, err := h.develop.SampleWBStaged(r.Context(), r.PathValue("sid"), rec, area)
	if err != nil {
		h.developError(w, err)
		return
	}
	h.jsonResponse(w, wb)
}

// Staged edits keep no history; the editor just shows an empty list.
func (h *Handlers) stagedDevelopHistory(w http.ResponseWriter, r *http.Request) {
	h.jsonResponse(w, map[string]interface{}{"items": []interface{}{}})
}
