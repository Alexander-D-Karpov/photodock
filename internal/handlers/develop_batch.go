package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/Alexander-D-Karpov/photodock/internal/develop"
)

const batchBodyLimit = 1 << 20

func (h *Handlers) registerDevelopBatchRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/api/presets", h.adminAuth(h.presetsList))
	mux.HandleFunc("POST /admin/api/presets", h.adminAuth(h.presetsSave))
	mux.HandleFunc("DELETE /admin/api/presets/{id}", h.adminAuth(h.presetsDelete))
	mux.HandleFunc("POST /admin/api/develop/batch", h.adminAuth(h.developBatch))
	mux.HandleFunc("POST /admin/api/develop/batch-reset", h.adminAuth(h.developBatchReset))
	mux.HandleFunc("POST /admin/api/develop/{id}/export", h.adminAuth(h.developExport))
	mux.HandleFunc("GET /admin/api/jobs/{id}", h.adminAuth(h.developJob))
	mux.HandleFunc("GET /admin/api/jobs/{id}/download", h.adminAuth(h.developJobDownload))
}

func decodeBody(w http.ResponseWriter, r *http.Request, limit int64, v interface{}) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

func (h *Handlers) presetsList(w http.ResponseWriter, r *http.Request) {
	items, err := h.develop.ListPresets(r.Context())
	if err != nil {
		h.developError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	h.jsonResponse(w, map[string]interface{}{"items": items})
}

func (h *Handlers) presetsSave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string          `json:"name"`
		Recipe  json.RawMessage `json:"recipe"`
		Modules []string        `json:"modules"`
	}
	if !decodeBody(w, r, developBodyLimit, &req) {
		return
	}
	rec, err := develop.ParseRecipe(req.Recipe)
	if err != nil {
		h.developError(w, err)
		return
	}
	p, err := h.develop.SavePreset(r.Context(), req.Name, rec, req.Modules)
	if err != nil {
		h.developError(w, err)
		return
	}
	h.jsonResponse(w, p)
}

func (h *Handlers) presetsDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := h.develop.DeletePreset(r.Context(), id); err != nil {
		h.developError(w, err)
		return
	}
	h.jsonResponse(w, map[string]interface{}{"ok": true})
}

func (h *Handlers) developBatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs     []int           `json:"ids"`
		Recipe  json.RawMessage `json:"recipe"`
		Modules []string        `json:"modules"`
		Label   string          `json:"label"`
	}
	if !decodeBody(w, r, batchBodyLimit, &req) {
		return
	}
	rec, err := develop.ParseRecipe(req.Recipe)
	if err != nil {
		h.developError(w, err)
		return
	}
	if len(req.Label) > 200 {
		req.Label = req.Label[:200]
	}
	res, err := h.develop.ApplyBatch(r.Context(), req.IDs, rec, req.Modules, req.Label)
	if err != nil {
		h.developError(w, err)
		return
	}
	h.jsonResponse(w, res)
}

func (h *Handlers) developBatchReset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []int `json:"ids"`
	}
	if !decodeBody(w, r, batchBodyLimit, &req) {
		return
	}
	res, err := h.develop.ResetBatch(r.Context(), req.IDs)
	if err != nil {
		h.developError(w, err)
		return
	}
	h.jsonResponse(w, res)
}

func (h *Handlers) developExport(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Recipe  json.RawMessage       `json:"recipe"`
		Options develop.ExportOptions `json:"options"`
	}
	if !decodeBody(w, r, developBodyLimit, &req) {
		return
	}
	rec, err := develop.ParseRecipe(req.Recipe)
	if err != nil {
		h.developError(w, err)
		return
	}
	job, err := h.develop.StartExport(r.Context(), id, rec, req.Options)
	if err != nil {
		h.developError(w, err)
		return
	}
	h.jsonResponse(w, job)
}

func (h *Handlers) developJob(w http.ResponseWriter, r *http.Request) {
	job, ok := h.develop.ExportJob(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	h.jsonResponse(w, job)
}

func (h *Handlers) developJobDownload(w http.ResponseWriter, r *http.Request) {
	path, name, ok := h.develop.ExportFile(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	// A large TIFF can outlast the server's 30 s WriteTimeout.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30 * time.Minute))
	ct := "image/jpeg"
	if strings.HasSuffix(name, ".tif") {
		ct = "image/tiff"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", contentDisposition("attachment", name))
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, path)
}
