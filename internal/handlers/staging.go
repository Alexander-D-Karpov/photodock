package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Alexander-D-Karpov/photodock/internal/services"
)

const (
	stagingMaxAge = 24 * time.Hour
	rawWebMaxAge  = 14 * 24 * time.Hour
	rawDevTimeout = 5 * time.Minute
)

func (h *Handlers) stagingDir(id string) string {
	return filepath.Join(h.cfg.CacheDir, "staging", id)
}

func stagingIDGen() string {
	return fmt.Sprintf("%d-%s", time.Now().UnixNano(), randString(8))
}

func isRawExt(name string) bool {
	return strings.HasSuffix(strings.ToLower(name), ".arw")
}

type stagedItem struct {
	ID         string `json:"id"`
	Seq        int64  `json:"seq"`
	Filename   string `json:"filename"`
	Size       int64  `json:"size"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	IsRaw      bool   `json:"is_raw"`
	Rotation   int    `json:"rotation"`
	PreviewURL string `json:"preview_url"`
	ViewURL    string `json:"view_url"`
}

func (it *stagedItem) setURLs() {
	v := "?r=" + strconv.Itoa(it.Rotation)
	it.PreviewURL = "/admin/staging/" + it.ID + "/preview" + v
	it.ViewURL = "/admin/staging/" + it.ID + "/view" + v
}

func (h *Handlers) stagingList(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Pool().Query(r.Context(),
		`SELECT id, seq, filename, size_bytes, width, height, is_raw, COALESCE(rotation, 0)
		 FROM staged_uploads ORDER BY seq, created_at, id`)
	if err != nil {
		h.jsonResponse(w, map[string]interface{}{"items": []stagedItem{}})
		return
	}
	defer rows.Close()

	items := []stagedItem{}
	for rows.Next() {
		var it stagedItem
		if err := rows.Scan(&it.ID, &it.Seq, &it.Filename, &it.Size, &it.Width, &it.Height, &it.IsRaw, &it.Rotation); err != nil {
			continue
		}
		it.setURLs()
		items = append(items, it)
	}
	h.jsonResponse(w, map[string]interface{}{"items": items})
}

func (h *Handlers) serveStagingFile(w http.ResponseWriter, r *http.Request, name string) {
	id := r.PathValue("id")
	if strings.Contains(id, "/") || strings.Contains(id, "..") {
		http.NotFound(w, r)
		return
	}
	p := filepath.Join(h.stagingDir(id), name)
	if _, err := os.Stat(p); err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, p)
}

func (h *Handlers) stagingPreview(w http.ResponseWriter, r *http.Request) {
	h.serveStagingFile(w, r, "preview.jpg")
}

func (h *Handlers) stagingView(w http.ResponseWriter, r *http.Request) {
	h.serveStagingFile(w, r, "view.jpg")
}

func (h *Handlers) stagingRotate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if strings.Contains(id, "/") || strings.Contains(id, "..") {
		http.NotFound(w, r)
		return
	}

	var it stagedItem
	err := h.db.Pool().QueryRow(r.Context(),
		`SELECT id, seq, filename, size_bytes, width, height, is_raw, COALESCE(rotation, 0)
		 FROM staged_uploads WHERE id = $1`, id).
		Scan(&it.ID, &it.Seq, &it.Filename, &it.Size, &it.Width, &it.Height, &it.IsRaw, &it.Rotation)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	delta := 90
	if r.FormValue("dir") == "ccw" {
		delta = -90
	}
	rotation := services.NormalizeRotation(it.Rotation + delta)

	dir := h.stagingDir(id)
	srcPath := filepath.Join(dir, it.Filename)
	pw, ph, err := h.thumbSvc.MakeStagingPreviews(srcPath, dir, rotation)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	if _, err := h.db.Pool().Exec(r.Context(),
		`UPDATE staged_uploads SET rotation = $1, width = $2, height = $3 WHERE id = $4`,
		rotation, pw, ph, id); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	it.Rotation = rotation
	it.Width = pw
	it.Height = ph
	it.setURLs()
	h.jsonResponse(w, it)
}

func (h *Handlers) finishStaging(ctx context.Context, id string, seq int64, filename, srcPath string) (*stagedItem, error) {
	info, err := os.Stat(srcPath)
	if err != nil {
		return nil, err
	}
	pw, ph, perr := h.thumbSvc.MakeStagingPreviews(srcPath, h.stagingDir(id), 0)
	if perr != nil {
		pw, ph = 0, 0
	}
	isRaw := isRawExt(filename)

	_, err = h.db.Pool().Exec(ctx,
		`INSERT INTO staged_uploads (id, seq, filename, size_bytes, width, height, is_raw, rotation)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, 0)`,
		id, seq, filename, info.Size(), pw, ph, isRaw)
	if err != nil {
		return nil, err
	}
	it := &stagedItem{
		ID: id, Seq: seq, Filename: filename, Size: info.Size(),
		Width: pw, Height: ph, IsRaw: isRaw, Rotation: 0,
	}
	it.setURLs()
	return it, nil
}

func (h *Handlers) stagingUploadSimple(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	defer func() { _ = file.Close() }()

	if !isImageFile(header.Filename) {
		http.Error(w, "Invalid file type", 400)
		return
	}

	seq, _ := strconv.ParseInt(r.FormValue("seq"), 10, 64)
	id := stagingIDGen()
	dir := h.stagingDir(id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	filename := sanitizeFilename(header.Filename)
	dstPath := filepath.Join(dir, filename)
	dst, err := os.Create(dstPath)
	if err != nil {
		_ = os.RemoveAll(dir)
		http.Error(w, err.Error(), 500)
		return
	}
	if _, err := io.Copy(dst, file); err != nil {
		_ = dst.Close()
		_ = os.RemoveAll(dir)
		http.Error(w, err.Error(), 500)
		return
	}
	_ = dst.Close()

	item, err := h.finishStaging(r.Context(), id, seq, filename, dstPath)
	if err != nil {
		_ = os.RemoveAll(dir)
		http.Error(w, err.Error(), 500)
		return
	}
	h.jsonResponse(w, item)
}

func (h *Handlers) stagingInit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Filename string `json:"filename"`
		Size     int64  `json:"size"`
		Seq      int64  `json:"seq"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if !isImageFile(req.Filename) {
		http.Error(w, "Invalid file type", 400)
		return
	}

	uploadID := stagingIDGen()
	tempDir := filepath.Join(h.cfg.CacheDir, "uploads", uploadID)
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	h.uploadsMux.Lock()
	h.uploads[uploadID] = &ChunkedUpload{
		ID:        uploadID,
		Filename:  sanitizeFilename(req.Filename),
		Size:      req.Size,
		Seq:       req.Seq,
		TempDir:   tempDir,
		Chunks:    make(map[int]bool),
		CreatedAt: time.Now(),
	}
	h.uploadsMux.Unlock()

	h.jsonResponse(w, map[string]string{"upload_id": uploadID})
}

func (h *Handlers) stagingFinalize(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UploadID string `json:"upload_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	h.uploadsMux.Lock()
	up, ok := h.uploads[req.UploadID]
	if ok {
		delete(h.uploads, req.UploadID)
	}
	h.uploadsMux.Unlock()
	if !ok {
		http.Error(w, "Upload not found", 404)
		return
	}
	defer func() { _ = os.RemoveAll(up.TempDir) }()

	id := stagingIDGen()
	dir := h.stagingDir(id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	dstPath := filepath.Join(dir, up.Filename)
	dst, err := os.Create(dstPath)
	if err != nil {
		_ = os.RemoveAll(dir)
		http.Error(w, err.Error(), 500)
		return
	}
	for i := 0; i < len(up.Chunks); i++ {
		chunk, err := os.Open(filepath.Join(up.TempDir, fmt.Sprintf("chunk_%d", i)))
		if err != nil {
			_ = dst.Close()
			_ = os.RemoveAll(dir)
			http.Error(w, err.Error(), 500)
			return
		}
		_, _ = io.Copy(dst, chunk)
		_ = chunk.Close()
	}
	_ = dst.Close()

	item, err := h.finishStaging(r.Context(), id, up.Seq, up.Filename, dstPath)
	if err != nil {
		_ = os.RemoveAll(dir)
		http.Error(w, err.Error(), 500)
		return
	}
	h.jsonResponse(w, item)
}

func (h *Handlers) stagingCommit(w http.ResponseWriter, r *http.Request) {
	var folderPath string
	if fidStr := r.FormValue("folder_id"); fidStr != "" && fidStr != "null" {
		fid, _ := strconv.Atoi(fidStr)
		_ = h.db.Pool().QueryRow(r.Context(), "SELECT path FROM folders WHERE id = $1", fid).Scan(&folderPath)
	}
	go h.commitStaged(context.Background(), folderPath)
	h.jsonResponse(w, map[string]string{"status": "started"})
}

func (h *Handlers) commitStaged(ctx context.Context, folderPath string) {
	var folderID *int
	if folderPath != "" {
		var fid int
		if err := h.db.Pool().QueryRow(ctx, "SELECT id FROM folders WHERE path = $1", folderPath).Scan(&fid); err == nil {
			folderID = &fid
		}
	}

	rows, err := h.db.Pool().Query(ctx,
		"SELECT id, filename, COALESCE(rotation, 0) FROM staged_uploads ORDER BY seq, created_at, id")
	if err != nil {
		log.Printf("commit staged query: %v", err)
		return
	}
	type item struct {
		id       string
		filename string
		rotation int
	}
	var items []item
	for rows.Next() {
		var it item
		if rows.Scan(&it.id, &it.filename, &it.rotation) == nil {
			items = append(items, it)
		}
	}
	rows.Close()

	for _, it := range items {
		func(it item) {
			defer func() {
				_, _ = h.db.Pool().Exec(ctx, "DELETE FROM staged_uploads WHERE id = $1", it.id)
				_ = os.RemoveAll(h.stagingDir(it.id))
			}()

			srcPath := filepath.Join(h.stagingDir(it.id), it.filename)
			relPath := it.filename
			if folderPath != "" {
				relPath = filepath.Join(folderPath, it.filename)
			}
			absDst := h.resolveConflict(filepath.Join(h.cfg.MediaRoot, relPath))
			if err := os.MkdirAll(filepath.Dir(absDst), 0755); err != nil {
				log.Printf("commit mkdir %s: %v", absDst, err)
				return
			}
			if err := moveFile(srcPath, absDst); err != nil {
				log.Printf("commit move %s: %v", srcPath, err)
				return
			}
			rel, rerr := filepath.Rel(h.cfg.MediaRoot, absDst)
			if rerr != nil {
				rel = relPath
			}
			if err := h.scanSvc.ImportFile(ctx, rel, folderID, it.rotation); err != nil {
				log.Printf("commit import %s: %v", rel, err)
			}
		}(it)
	}
	log.Printf("commit staged complete: %d files", len(items))
}

func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		_ = in.Close()
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = in.Close()
		return err
	}
	if err := out.Close(); err != nil {
		_ = in.Close()
		return err
	}
	_ = in.Close()
	return os.Remove(src)
}

func (h *Handlers) stagingDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if strings.Contains(id, "/") || strings.Contains(id, "..") {
		http.NotFound(w, r)
		return
	}
	_, _ = h.db.Pool().Exec(r.Context(), "DELETE FROM staged_uploads WHERE id = $1", id)
	_ = os.RemoveAll(h.stagingDir(id))
	w.WriteHeader(http.StatusOK)
}

func (h *Handlers) CleanupStaging(ctx context.Context) {
	rows, err := h.db.Pool().Query(ctx,
		"SELECT id FROM staged_uploads WHERE created_at < $1", time.Now().Add(-stagingMaxAge))
	if err == nil {
		var ids []string
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		rows.Close()
		for _, id := range ids {
			_, _ = h.db.Pool().Exec(ctx, "DELETE FROM staged_uploads WHERE id = $1", id)
			_ = os.RemoveAll(h.stagingDir(id))
		}
	}

	base := filepath.Join(h.cfg.CacheDir, "staging")
	if entries, err := os.ReadDir(base); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			var exists bool
			_ = h.db.Pool().QueryRow(ctx,
				"SELECT EXISTS(SELECT 1 FROM staged_uploads WHERE id = $1)", e.Name()).Scan(&exists)
			if !exists {
				_ = os.RemoveAll(filepath.Join(base, e.Name()))
			}
		}
	}

	h.uploadsMux.Lock()
	for id, up := range h.uploads {
		if time.Since(up.CreatedAt) > stagingMaxAge {
			_ = os.RemoveAll(up.TempDir)
			delete(h.uploads, id)
		}
	}
	h.uploadsMux.Unlock()

	scratch := filepath.Join(h.cfg.CacheDir, "raw")
	if entries, err := os.ReadDir(scratch); err == nil {
		rawDevTimeoutSlack := rawDevTimeout
		cutoff := time.Now().Add(-2 * rawDevTimeoutSlack)
		for _, e := range entries {
			info, ierr := e.Info()
			if ierr != nil || info.ModTime().After(cutoff) {
				continue
			}
			_ = os.RemoveAll(filepath.Join(scratch, e.Name()))
		}
	}

	rawDir := filepath.Join(h.cfg.CacheDir, "rawdev")
	if entries, err := os.ReadDir(rawDir); err == nil {
		cutoff := time.Now().Add(-rawWebMaxAge)
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			info, ierr := e.Info()
			if ierr != nil {
				continue
			}
			if strings.HasSuffix(e.Name(), ".tmp") || info.ModTime().Before(cutoff) {
				_ = os.Remove(filepath.Join(rawDir, e.Name()))
			}
		}
	}
}

func (h *Handlers) StartJanitor(ctx context.Context) {
	go func() {
		h.CleanupStaging(ctx)
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				h.CleanupStaging(ctx)
			}
		}
	}()
}
