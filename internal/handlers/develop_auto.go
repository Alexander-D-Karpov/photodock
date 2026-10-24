package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/Alexander-D-Karpov/photodock/internal/develop"
)

func (h *Handlers) registerDevelopAutoRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /admin/api/develop/{id}/auto", h.adminAuth(h.developAuto))
	mux.HandleFunc("POST /admin/api/staged-develop/{sid}/auto", h.adminAuth(h.stagedDevelopAuto))
	mux.HandleFunc("POST /admin/api/develop/batch-auto", h.adminAuth(h.developBatchAuto))
	mux.HandleFunc("POST /admin/api/develop/{id}/warm", h.adminAuth(h.developWarm))
	mux.HandleFunc("POST /admin/api/staged-develop/{sid}/warm", h.adminAuth(h.stagedDevelopWarm))
}

func (h *Handlers) readAuto(w http.ResponseWriter, r *http.Request) (develop.Recipe, develop.AutoOptions, bool) {
	var req struct {
		Recipe  json.RawMessage     `json:"recipe"`
		Options develop.AutoOptions `json:"options"`
	}
	if !decodeBody(w, r, developBodyLimit, &req) {
		return develop.Recipe{}, develop.AutoOptions{}, false
	}
	rec, err := develop.ParseRecipe(req.Recipe)
	if err != nil {
		h.developError(w, err)
		return develop.Recipe{}, develop.AutoOptions{}, false
	}
	return rec, req.Options, true
}

func (h *Handlers) developAuto(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	rec, opts, ok := h.readAuto(w, r)
	if !ok {
		return
	}
	res, err := h.develop.Auto(r.Context(), id, rec, opts)
	if err != nil {
		h.developError(w, err)
		return
	}
	h.jsonResponse(w, res)
}

func (h *Handlers) stagedDevelopAuto(w http.ResponseWriter, r *http.Request) {
	rec, opts, ok := h.readAuto(w, r)
	if !ok {
		return
	}
	res, err := h.develop.AutoStaged(r.Context(), r.PathValue("sid"), rec, opts)
	if err != nil {
		h.developError(w, err)
		return
	}
	h.jsonResponse(w, res)
}

func (h *Handlers) developBatchAuto(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []int `json:"ids"`
		WB  bool  `json:"wb"`
	}
	if !decodeBody(w, r, batchBodyLimit, &req) {
		return
	}
	if err := h.develop.AutoBatch(req.IDs, develop.AutoOptions{WB: req.WB}); err != nil {
		h.developError(w, err)
		return
	}
	h.jsonResponse(w, map[string]interface{}{"started": len(req.IDs)})
}

func (h *Handlers) readWarm(w http.ResponseWriter, r *http.Request) (develop.Recipe, bool) {
	var req struct {
		Recipe json.RawMessage `json:"recipe"`
	}
	if !decodeBody(w, r, developBodyLimit, &req) {
		return develop.Recipe{}, false
	}
	rec, err := develop.ParseRecipe(req.Recipe)
	if err != nil {
		h.developError(w, err)
		return develop.Recipe{}, false
	}
	return rec, true
}

func (h *Handlers) developWarm(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	rec, ok := h.readWarm(w, r)
	if !ok {
		return
	}
	h.develop.Warm(id, rec)
	w.WriteHeader(http.StatusAccepted)
}

func (h *Handlers) stagedDevelopWarm(w http.ResponseWriter, r *http.Request) {
	rec, ok := h.readWarm(w, r)
	if !ok {
		return
	}
	h.develop.WarmStaged(r.PathValue("sid"), rec)
	w.WriteHeader(http.StatusAccepted)
}
