package handlers

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Alexander-D-Karpov/photodock/internal/config"
	"github.com/Alexander-D-Karpov/photodock/internal/database"
	"github.com/Alexander-D-Karpov/photodock/internal/models"
	"github.com/Alexander-D-Karpov/photodock/internal/services"
)

type Handlers struct {
	db         *database.DB
	cfg        *config.Config
	thumbSvc   *services.ThumbnailService
	scanSvc    *services.ScannerService
	tmpl       *template.Template
	webFS      embed.FS
	uploads    map[string]*ChunkedUpload
	uploadsMux sync.RWMutex
}

type ChunkedUpload struct {
	ID        string
	Filename  string
	Size      int64
	FolderID  *int
	TempDir   string
	Chunks    map[int]bool
	CreatedAt time.Time
}

type IntPtrOrString struct {
	V *int
}

func New(db *database.DB, cfg *config.Config, thumbSvc *services.ThumbnailService, scanSvc *services.ScannerService, webFS embed.FS) *Handlers {
	funcMap := template.FuncMap{
		"json": func(v interface{}) template.JS {
			b, _ := json.Marshal(v)
			return template.JS(b)
		},
		"formatSize": formatSize,
		"formatDate": func(t time.Time) string {
			return t.Format("2006-01-02 15:04")
		},
		"add":       func(a, b int) int { return a + b },
		"sub":       func(a, b int) int { return a - b },
		"int64":     func(i int) int64 { return int64(i) },
		"urlpath":   escapeURLPath,
		"hasPrefix": strings.HasPrefix,
		"iterate": func(n int) []int {
			result := make([]int, n)
			for i := range result {
				result[i] = i
			}
			return result
		},
		"divf": func(a, b int) float64 {
			if b == 0 {
				return 1.0
			}
			return float64(a) / float64(b)
		},
	}

	tmplFS, _ := fs.Sub(webFS, "web/templates")
	tmpl := template.New("").Funcs(funcMap)

	err := fs.WalkDir(tmplFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".html") {
			return err
		}
		content, err := fs.ReadFile(tmplFS, path)
		if err != nil {
			return err
		}
		_, err = tmpl.New(path).Parse(string(content))
		return err
	})
	if err != nil {
		log.Printf("template walk error: %v", err)
	}

	return &Handlers{
		db:       db,
		cfg:      cfg,
		thumbSvc: thumbSvc,
		scanSvc:  scanSvc,
		tmpl:     tmpl,
		webFS:    webFS,
		uploads:  make(map[string]*ChunkedUpload),
	}
}

func (x *IntPtrOrString) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))

	if s == "null" {
		x.V = nil
		return nil
	}

	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		u, err := strconv.Unquote(s)
		if err != nil {
			return err
		}
		u = strings.TrimSpace(u)
		if u == "" || u == "null" {
			x.V = nil
			return nil
		}
		i, err := strconv.Atoi(u)
		if err != nil {
			return err
		}
		x.V = &i
		return nil
	}

	i64, err := strconv.ParseInt(s, 10, 0)
	if err != nil {
		return err
	}
	i := int(i64)
	x.V = &i
	return nil
}

func (h *Handlers) RegisterRoutes(mux *http.ServeMux) {
	staticFS, _ := fs.Sub(h.webFS, "web/static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	mux.HandleFunc("GET /", h.publicIndex)
	mux.HandleFunc("GET /folder/{id}", h.publicFolder)
	mux.HandleFunc("GET /p/{path...}", h.publicPath)
	mux.HandleFunc("GET /photo/{id}", h.publicPhotoByID)
	mux.HandleFunc("GET /thumb/{size}/{id}", h.serveThumbnail)
	mux.HandleFunc("GET /original/{id}", h.serveOriginal)
	mux.HandleFunc("GET /placeholder/{id}", h.servePlaceholder)

	mux.HandleFunc("GET /admin", h.adminAuth(h.adminDashboard))
	mux.HandleFunc("GET /admin/folders", h.adminAuth(h.adminFolders))
	mux.HandleFunc("POST /admin/folders", h.adminAuth(h.adminCreateFolder))
	mux.HandleFunc("GET /admin/folders/{id}", h.adminAuth(h.adminEditFolder))
	mux.HandleFunc("POST /admin/folders/{id}", h.adminAuth(h.adminUpdateFolder))
	mux.HandleFunc("DELETE /admin/folders/{id}", h.adminAuth(h.adminDeleteFolder))
	mux.HandleFunc("POST /admin/folders/{id}/cover", h.adminAuth(h.adminSetCover))
	mux.HandleFunc("GET /admin/photos", h.adminAuth(h.adminPhotos))
	mux.HandleFunc("GET /admin/photos/{id}", h.adminAuth(h.adminEditPhoto))
	mux.HandleFunc("POST /admin/photos/{id}", h.adminAuth(h.adminUpdatePhoto))
	mux.HandleFunc("DELETE /admin/photos/{id}", h.adminAuth(h.adminDeletePhoto))
	mux.HandleFunc("POST /admin/photos/{id}/hide", h.adminAuth(h.adminToggleHide))
	mux.HandleFunc("POST /admin/photos/{id}/move", h.adminAuth(h.adminMovePhoto))
	mux.HandleFunc("POST /admin/scan", h.adminAuth(h.adminScan))
	mux.HandleFunc("POST /admin/scan/{id}", h.adminAuth(h.adminScanFolder))
	mux.HandleFunc("POST /admin/clean", h.adminAuth(h.adminClean))
	mux.HandleFunc("POST /admin/regenerate-urls", h.adminAuth(h.adminRegenerateURLs))
	mux.HandleFunc("POST /admin/upload", h.adminAuth(h.adminUpload))
	mux.HandleFunc("POST /admin/upload/file", h.adminAuth(h.adminUploadFile))
	mux.HandleFunc("POST /admin/upload/init", h.adminAuth(h.adminUploadInit))
	mux.HandleFunc("POST /admin/upload/chunk", h.adminAuth(h.adminUploadChunk))
	mux.HandleFunc("POST /admin/upload/finalize", h.adminAuth(h.adminUploadFinalize))

	mux.HandleFunc("GET /api/folders", h.apiListFolders)
	mux.HandleFunc("GET /api/folders/{id}", h.apiGetFolder)
	mux.HandleFunc("GET /api/photos", h.apiListPhotos)
	mux.HandleFunc("GET /api/photos/{id}", h.apiGetPhoto)
}

func (h *Handlers) adminAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != h.cfg.AdminUser || pass != h.cfg.AdminPass {
			w.Header().Set("WWW-Authenticate", `Basic realm="Admin"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (h *Handlers) publicIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	ctx := r.Context()

	if r.URL.Query().Get("ajax") == "1" {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		h.jsonPhotosPage(w, r, ctx, nil, page)
		return
	}

	folders, _ := h.getRootFolders(ctx)
	photos, _ := h.getRootPhotos(ctx)

	var photoCount, folderCount int
	var totalSize int64
	_ = h.db.Pool().QueryRow(ctx, "SELECT COUNT(*) FROM photos WHERE hidden = false").Scan(&photoCount)
	_ = h.db.Pool().QueryRow(ctx, "SELECT COUNT(*) FROM folders WHERE parent_id IS NULL").Scan(&folderCount)
	_ = h.db.Pool().QueryRow(ctx, "SELECT COALESCE(SUM(size_bytes), 0) FROM photos WHERE hidden = false").Scan(&totalSize)

	h.render(w, "public/index.html", map[string]interface{}{
		"Folders":     folders,
		"Photos":      photos,
		"Title":       "Index",
		"PhotoCount":  photoCount,
		"FolderCount": folderCount,
		"TotalSize":   totalSize,
	})
}

func (h *Handlers) jsonPhotosPage(w http.ResponseWriter, r *http.Request, ctx context.Context, folderID *int, page int) {
	const perPage = 50
	offset := (page - 1) * perPage

	var where string
	var args []interface{}

	if folderID != nil {
		where = "folder_id = $1 AND hidden = false"
		args = []interface{}{*folderID, perPage, offset}
	} else {
		where = "folder_id IS NULL AND hidden = false"
		args = []interface{}{perPage, offset}
	}

	query := fmt.Sprintf(`
		SELECT id, filename, COALESCE(url_path, ''), title, size_bytes, blurhash, 
		       COALESCE(EXTRACT(EPOCH FROM taken_at), EXTRACT(EPOCH FROM created_at))::bigint as date
		FROM photos WHERE %s 
		ORDER BY COALESCE(taken_at, created_at) DESC, id DESC 
		LIMIT $%d OFFSET $%d`, where, len(args)-1, len(args))

	if folderID != nil {
		args = []interface{}{*folderID, perPage, offset}
	} else {
		args = []interface{}{perPage, offset}
	}

	rows, err := h.db.Pool().Query(ctx, query, args...)
	if err != nil {
		h.jsonResponse(w, map[string]interface{}{"photos": []interface{}{}, "hasMore": false})
		return
	}
	defer rows.Close()

	type photoJSON struct {
		ID       int    `json:"id"`
		Filename string `json:"filename"`
		URL      string `json:"url"`
		Title    string `json:"title"`
		Size     int64  `json:"size"`
		Blurhash string `json:"blurhash"`
		Date     int64  `json:"date"`
	}

	var photos []photoJSON
	for rows.Next() {
		var p photoJSON
		var urlPath string
		var title sql.NullString
		var blurhash sql.NullString

		if err := rows.Scan(&p.ID, &p.Filename, &urlPath, &title, &p.Size, &blurhash, &p.Date); err != nil {
			continue
		}

		if urlPath != "" {
			p.URL = "/p/" + urlPath
		} else {
			p.URL = fmt.Sprintf("/photo/%d", p.ID)
		}
		if title.Valid {
			p.Title = title.String
		}
		if blurhash.Valid {
			p.Blurhash = blurhash.String
		}
		photos = append(photos, p)
	}

	var totalCount int
	if folderID != nil {
		_ = h.db.Pool().QueryRow(ctx, "SELECT COUNT(*) FROM photos WHERE folder_id = $1 AND hidden = false", *folderID).Scan(&totalCount)
	} else {
		_ = h.db.Pool().QueryRow(ctx, "SELECT COUNT(*) FROM photos WHERE folder_id IS NULL AND hidden = false").Scan(&totalCount)
	}

	hasMore := page*perPage < totalCount

	h.jsonResponse(w, map[string]interface{}{
		"photos":  photos,
		"hasMore": hasMore,
		"page":    page,
		"total":   totalCount,
	})
}

func (h *Handlers) publicFolder(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.PathValue("id"))
	ctx := r.Context()

	var folderPath string
	if err := h.db.Pool().QueryRow(ctx, "SELECT path FROM folders WHERE id = $1", id).Scan(&folderPath); err != nil {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/p/"+escapeURLPath(folderPath)+"/", http.StatusMovedPermanently)
}

func (h *Handlers) publicPath(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("path")
	if raw == "" {
		http.Redirect(w, r, "/", http.StatusMovedPermanently)
		return
	}

	isFolderReq := strings.HasSuffix(r.URL.Path, "/")
	cleaned := strings.Trim(raw, "/")
	if cleaned == "" {
		http.Redirect(w, r, "/", http.StatusMovedPermanently)
		return
	}

	if isFolderReq {
		folder, err := h.getFolderByPath(r.Context(), cleaned)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		h.renderFolder(w, r, folder)
		return
	}

	if _, err := h.getFolderByPath(r.Context(), cleaned); err == nil {
		http.Redirect(w, r, "/p/"+escapeURLPath(cleaned)+"/", http.StatusMovedPermanently)
		return
	}

	photo, err := h.getPhotoByURLPath(r.Context(), cleaned)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h.renderPhoto(w, r, photo)
}

func (h *Handlers) getFolderByPath(ctx context.Context, path string) (*models.Folder, error) {
	var folder models.Folder
	err := h.db.Pool().QueryRow(ctx,
		"SELECT id, parent_id, name, path FROM folders WHERE path = $1", path).
		Scan(&folder.ID, &folder.ParentID, &folder.Name, &folder.Path)
	if err != nil {
		return nil, err
	}
	return &folder, nil
}

func (h *Handlers) renderFolder(w http.ResponseWriter, r *http.Request, folder *models.Folder) {
	ctx := r.Context()

	subfolders, _ := h.getSubfolders(ctx, folder.ID)
	photos, _ := h.getFolderPhotos(ctx, folder.ID)
	breadcrumbs := h.getBreadcrumbs(ctx, folder)

	parentURL := "/"
	if folder.ParentID.Valid {
		var parentPath string
		if err := h.db.Pool().QueryRow(ctx, "SELECT path FROM folders WHERE id = $1", folder.ParentID.Int64).Scan(&parentPath); err == nil {
			parentURL = "/p/" + escapeURLPath(parentPath) + "/"
		}
	}

	h.render(w, "public/folder.html", map[string]interface{}{
		"Folder":      *folder,
		"Subfolders":  subfolders,
		"Photos":      photos,
		"Breadcrumbs": breadcrumbs,
		"ParentURL":   parentURL,
		"Title":       folder.Name,
	})
}

func (h *Handlers) publicPhotoByID(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.PathValue("id"))

	photo, err := h.getPhotoByID(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if photo.URLPath != "" {
		http.Redirect(w, r, "/p/"+photo.URLPath, http.StatusMovedPermanently)
		return
	}

	h.renderPhoto(w, r, photo)
}

func (h *Handlers) renderPhoto(w http.ResponseWriter, r *http.Request, photo *models.Photo) {
	ctx := r.Context()

	var exifInfo models.ExifInfo
	if photo.ExifData != nil {
		_ = json.Unmarshal(photo.ExifData, &exifInfo)
	}

	prevURL, nextURL, prevID, nextID := h.getAdjacentPhotoInfo(ctx, photo)
	breadcrumbs := h.getPhotoBreadcrumbs(ctx, photo)
	position, total := h.getPhotoPosition(ctx, photo)

	title := photo.Filename
	if photo.Title.Valid && photo.Title.String != "" {
		title = photo.Title.String
	}

	folderURL := "/"
	if len(breadcrumbs) > 0 {
		folderURL = "/p/" + escapeURLPath(breadcrumbs[len(breadcrumbs)-1].Path) + "/"
	}

	baseURL := "https://" + r.Host
	if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" {
		baseURL = "http://" + r.Host
	}

	previewWidth := 1920
	previewHeight := 0
	if photo.Width > 0 && photo.Height > 0 {
		previewHeight = int(float64(photo.Height) * (float64(previewWidth) / float64(photo.Width)))
		if photo.Width < previewWidth {
			previewWidth = photo.Width
			previewHeight = photo.Height
		}
	}

	h.render(w, "public/photo.html", map[string]interface{}{
		"Photo":         photo,
		"ExifInfo":      exifInfo,
		"PrevURL":       prevURL,
		"NextURL":       nextURL,
		"PrevID":        prevID,
		"NextID":        nextID,
		"Breadcrumbs":   breadcrumbs,
		"Title":         title,
		"FolderURL":     folderURL,
		"PhotoPosition": position,
		"PhotoTotal":    total,
		"BaseURL":       baseURL,
		"PreviewWidth":  previewWidth,
		"PreviewHeight": previewHeight,
	})
}

func escapeURLPath(p string) string {
	parts := strings.Split(p, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}

func (h *Handlers) serveThumbnail(w http.ResponseWriter, r *http.Request) {
	size := r.PathValue("size")
	id, _ := strconv.Atoi(r.PathValue("id"))

	if size != "small" && size != "medium" && size != "large" {
		http.NotFound(w, r)
		return
	}

	var path string
	if err := h.db.Pool().QueryRow(r.Context(), "SELECT path FROM photos WHERE id = $1", id).Scan(&path); err != nil {
		http.NotFound(w, r)
		return
	}

	thumbPath, err := h.thumbSvc.GetThumbnailPathByID(id, path, size)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")

	if r.Header.Get("X-Real-IP") != "" {
		w.Header().Set("X-Accel-Redirect", fmt.Sprintf("/internal/cache/%s/%d%s", size, id, filepath.Ext(thumbPath)))
		contentType := "image/jpeg"
		if strings.HasSuffix(strings.ToLower(path), ".png") {
			contentType = "image/png"
		}
		w.Header().Set("Content-Type", contentType)
		return
	}

	http.ServeFile(w, r, thumbPath)
}

func (h *Handlers) servePlaceholder(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.PathValue("id"))

	var blurhash string
	if err := h.db.Pool().QueryRow(r.Context(), "SELECT COALESCE(blurhash, '') FROM photos WHERE id = $1", id).Scan(&blurhash); err != nil {
		http.NotFound(w, r)
		return
	}

	placeholderPath, err := h.thumbSvc.GetPlaceholderPathByID(id, blurhash)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")

	if r.Header.Get("X-Real-IP") != "" {
		w.Header().Set("X-Accel-Redirect", fmt.Sprintf("/internal/cache/placeholder/%d.png", id))
		w.Header().Set("Content-Type", "image/png")
		return
	}

	http.ServeFile(w, r, placeholderPath)
}

func (h *Handlers) adminDeletePhoto(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.PathValue("id"))
	ctx := r.Context()

	var path string
	_ = h.db.Pool().QueryRow(ctx, "SELECT path FROM photos WHERE id = $1", id).Scan(&path)
	_, _ = h.db.Pool().Exec(ctx, "DELETE FROM photos WHERE id = $1", id)

	if path != "" {
		_ = h.thumbSvc.DeleteThumbnailsByID(id)
		_ = os.Remove(filepath.Join(h.cfg.MediaRoot, path))
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handlers) serveOriginal(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.PathValue("id"))

	var path string
	var hidden bool
	err := h.db.Pool().QueryRow(r.Context(), "SELECT path, hidden FROM photos WHERE id = $1", id).Scan(&path, &hidden)
	if err != nil || hidden || !h.isPathSafe(path) {
		http.NotFound(w, r)
		return
	}

	if r.Header.Get("X-Real-IP") != "" {
		w.Header().Set("X-Accel-Redirect", "/internal/photos/"+path)
		w.Header().Set("Content-Type", "image/jpeg")
		if strings.HasSuffix(strings.ToLower(path), ".png") {
			w.Header().Set("Content-Type", "image/png")
		}
		return
	}

	w.Header().Set("Cache-Control", "public, max-age=31536000")
	http.ServeFile(w, r, filepath.Join(h.cfg.MediaRoot, path))
}

func (h *Handlers) adminDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var photoCount, folderCount, hiddenCount int
	var totalSize int64

	_ = h.db.Pool().QueryRow(ctx, "SELECT COUNT(*) FROM photos").Scan(&photoCount)
	_ = h.db.Pool().QueryRow(ctx, "SELECT COUNT(*) FROM folders").Scan(&folderCount)
	_ = h.db.Pool().QueryRow(ctx, "SELECT COUNT(*) FROM photos WHERE hidden = true").Scan(&hiddenCount)
	_ = h.db.Pool().QueryRow(ctx, "SELECT COALESCE(SUM(size_bytes), 0) FROM photos").Scan(&totalSize)

	folders, _ := h.getAllFolders(ctx)

	h.render(w, "admin/dashboard.html", map[string]interface{}{
		"PhotoCount":  photoCount,
		"FolderCount": folderCount,
		"HiddenCount": hiddenCount,
		"TotalSize":   totalSize,
		"Folders":     folders,
		"Title":       "Admin Dashboard",
	})
}

func (h *Handlers) adminFolders(w http.ResponseWriter, r *http.Request) {
	folders, err := h.getFolderTree(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	h.render(w, "admin/folders.html", map[string]interface{}{
		"Folders": folders,
		"Title":   "Manage Folders",
	})
}

func (h *Handlers) adminCreateFolder(w http.ResponseWriter, r *http.Request) {
	name := sanitizeFilename(r.FormValue("name"))
	if name == "" || name == "." || name == ".." {
		http.Error(w, "Invalid name", 400)
		return
	}

	ctx := r.Context()
	var parentID *int
	var parentPath string

	if pidStr := r.FormValue("parent_id"); pidStr != "" {
		pid, _ := strconv.Atoi(pidStr)
		parentID = &pid
		_ = h.db.Pool().QueryRow(ctx, "SELECT path FROM folders WHERE id = $1", pid).Scan(&parentPath)
	}

	path := name
	if parentPath != "" {
		path = filepath.Join(parentPath, name)
	}

	if err := os.MkdirAll(filepath.Join(h.cfg.MediaRoot, path), 0755); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	_, _ = h.db.Pool().Exec(ctx,
		"INSERT INTO folders (parent_id, name, path) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING",
		parentID, name, path)

	http.Redirect(w, r, "/admin/folders", http.StatusSeeOther)
}

func (h *Handlers) adminEditFolder(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.PathValue("id"))
	ctx := r.Context()

	var folder models.Folder
	err := h.db.Pool().QueryRow(ctx,
		"SELECT id, parent_id, name, path, cover_photo_id FROM folders WHERE id = $1", id).
		Scan(&folder.ID, &folder.ParentID, &folder.Name, &folder.Path, &folder.CoverPhotoID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	photos, _ := h.getFolderPhotos(ctx, id)
	allFolders, _ := h.getAllFolders(ctx)

	h.render(w, "admin/folder_edit.html", map[string]interface{}{
		"Folder":     folder,
		"Photos":     photos,
		"AllFolders": allFolders,
		"Title":      "Edit " + folder.Name,
	})
}

func (h *Handlers) adminUpdateFolder(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.PathValue("id"))
	name := sanitizeFilename(r.FormValue("name"))

	if name == "" || name == "." || name == ".." {
		http.Error(w, "Invalid name", 400)
		return
	}

	_, _ = h.db.Pool().Exec(r.Context(), "UPDATE folders SET name = $1, updated_at = NOW() WHERE id = $2", name, id)
	http.Redirect(w, r, "/admin/folders", http.StatusSeeOther)
}

func (h *Handlers) adminDeleteFolder(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.PathValue("id"))
	_, _ = h.db.Pool().Exec(r.Context(), "DELETE FROM folders WHERE id = $1", id)
	w.WriteHeader(http.StatusOK)
}

func (h *Handlers) adminSetCover(w http.ResponseWriter, r *http.Request) {
	folderID, _ := strconv.Atoi(r.PathValue("id"))

	var photoID *int
	if pidStr := r.FormValue("photo_id"); pidStr != "" {
		pid, _ := strconv.Atoi(pidStr)
		photoID = &pid
	}

	_, _ = h.db.Pool().Exec(r.Context(),
		"UPDATE folders SET cover_photo_id = $1, updated_at = NOW() WHERE id = $2",
		photoID, folderID)
	w.WriteHeader(http.StatusOK)
}

func (h *Handlers) adminPhotos(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}

	const perPage = 50
	offset := (page - 1) * perPage
	folderFilter := r.URL.Query().Get("folder")
	showHidden := r.URL.Query().Get("hidden") == "1"
	searchQuery := r.URL.Query().Get("q")

	query := "SELECT id, folder_id, filename, path, title, hidden, width, height FROM photos WHERE 1=1"
	countQuery := "SELECT COUNT(*) FROM photos WHERE 1=1"
	var args []interface{}
	argIdx := 1

	if searchQuery != "" {
		query += fmt.Sprintf(" AND (filename ILIKE $%d OR title ILIKE $%d OR description ILIKE $%d)", argIdx, argIdx, argIdx)
		countQuery += fmt.Sprintf(" AND (filename ILIKE $%d OR title ILIKE $%d OR description ILIKE $%d)", argIdx, argIdx, argIdx)
		args = append(args, "%"+searchQuery+"%")
		argIdx++
	}

	if folderFilter == "root" {
		query += " AND folder_id IS NULL"
		countQuery += " AND folder_id IS NULL"
	} else if folderFilter != "" {
		fid, _ := strconv.Atoi(folderFilter)
		query += fmt.Sprintf(" AND folder_id = $%d", argIdx)
		countQuery += fmt.Sprintf(" AND folder_id = $%d", argIdx)
		args = append(args, fid)
		argIdx++
	}

	if !showHidden {
		query += " AND hidden = false"
		countQuery += " AND hidden = false"
	}

	var totalCount int
	_ = h.db.Pool().QueryRow(ctx, countQuery, args...).Scan(&totalCount)

	query += fmt.Sprintf(" ORDER BY COALESCE(taken_at, created_at) DESC, id DESC LIMIT $%d OFFSET $%d", argIdx, argIdx+1)
	args = append(args, perPage, offset)

	rows, _ := h.db.Pool().Query(ctx, query, args...)
	defer rows.Close()

	var photos []models.Photo
	for rows.Next() {
		var p models.Photo
		if err := rows.Scan(&p.ID, &p.FolderID, &p.Filename, &p.Path, &p.Title, &p.Hidden, &p.Width, &p.Height); err != nil {
			continue
		}
		photos = append(photos, p)
	}

	folders, _ := h.getAllFolders(ctx)

	h.render(w, "admin/photos.html", map[string]interface{}{
		"Photos":       photos,
		"Folders":      folders,
		"CurrentPage":  page,
		"TotalPages":   (totalCount + perPage - 1) / perPage,
		"TotalCount":   totalCount,
		"FolderFilter": folderFilter,
		"ShowHidden":   showHidden,
		"SearchQuery":  searchQuery,
		"Title":        "Manage Photos",
	})
}

func (h *Handlers) adminEditPhoto(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.PathValue("id"))
	ctx := r.Context()

	var photo models.Photo
	err := h.db.Pool().QueryRow(ctx,
		`SELECT id, folder_id, filename, path, COALESCE(url_path, ''), title, description, note, 
		width, height, size_bytes, exif_data, hidden, created_at, taken_at 
		FROM photos WHERE id = $1`, id).
		Scan(&photo.ID, &photo.FolderID, &photo.Filename, &photo.Path, &photo.URLPath,
			&photo.Title, &photo.Description, &photo.Note,
			&photo.Width, &photo.Height, &photo.SizeBytes,
			&photo.ExifData, &photo.Hidden, &photo.CreatedAt, &photo.TakenAt)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var exifInfo models.ExifInfo
	if photo.ExifData != nil {
		_ = json.Unmarshal(photo.ExifData, &exifInfo)
	}

	folders, _ := h.getAllFolders(ctx)

	h.render(w, "admin/photo_edit.html", map[string]interface{}{
		"Photo":    photo,
		"ExifInfo": exifInfo,
		"Folders":  folders,
		"Title":    "Edit " + photo.Filename,
	})
}

func (h *Handlers) adminUpdatePhoto(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.PathValue("id"))

	var folderID *int
	if fidStr := r.FormValue("folder_id"); fidStr != "" && fidStr != "null" {
		fid, _ := strconv.Atoi(fidStr)
		folderID = &fid
	}

	_, _ = h.db.Pool().Exec(r.Context(),
		`UPDATE photos SET title = NULLIF($1, ''), description = NULLIF($2, ''), 
		note = NULLIF($3, ''), folder_id = $4, updated_at = NOW() WHERE id = $5`,
		r.FormValue("title"), r.FormValue("description"), r.FormValue("note"), folderID, id)

	http.Redirect(w, r, fmt.Sprintf("/admin/photos/%d", id), http.StatusSeeOther)
}

func (h *Handlers) adminToggleHide(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.PathValue("id"))
	_, _ = h.db.Pool().Exec(r.Context(), "UPDATE photos SET hidden = NOT hidden, updated_at = NOW() WHERE id = $1", id)
	w.WriteHeader(http.StatusOK)
}

func (h *Handlers) adminMovePhoto(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.PathValue("id"))

	var folderID *int
	if fidStr := r.FormValue("folder_id"); fidStr != "" {
		fid, _ := strconv.Atoi(fidStr)
		if fid > 0 {
			folderID = &fid
		}
	}

	_, _ = h.db.Pool().Exec(r.Context(), "UPDATE photos SET folder_id = $1, updated_at = NOW() WHERE id = $2", folderID, id)
	w.WriteHeader(http.StatusOK)
}

func (h *Handlers) adminScan(w http.ResponseWriter, r *http.Request) {
	go func() {
		_ = h.scanSvc.ScanAll(context.Background())
	}()
	h.jsonResponse(w, map[string]string{"status": "started"})
}

func (h *Handlers) adminScanFolder(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.PathValue("id"))

	var path string
	if err := h.db.Pool().QueryRow(r.Context(), "SELECT path FROM folders WHERE id = $1", id).Scan(&path); err != nil {
		http.NotFound(w, r)
		return
	}

	go func() {
		_ = h.scanSvc.ScanFolder(context.Background(), path)
	}()
	h.jsonResponse(w, map[string]string{"status": "started"})
}

func (h *Handlers) adminClean(w http.ResponseWriter, r *http.Request) {
	go func() {
		_ = h.scanSvc.CleanOrphans(context.Background())
	}()
	h.jsonResponse(w, map[string]string{"status": "started"})
}

func (h *Handlers) adminRegenerateURLs(w http.ResponseWriter, r *http.Request) {
	go func() {
		_ = h.scanSvc.RegenerateURLPaths(context.Background())
	}()
	h.jsonResponse(w, map[string]string{"status": "started"})
}

func (h *Handlers) adminUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(100 << 20); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	ctx := r.Context()
	var folderPath string
	if fidStr := r.FormValue("folder_id"); fidStr != "" && fidStr != "null" {
		fid, _ := strconv.Atoi(fidStr)
		_ = h.db.Pool().QueryRow(ctx, "SELECT path FROM folders WHERE id = $1", fid).Scan(&folderPath)
	}

	for _, fh := range r.MultipartForm.File["files"] {
		if !isImageFile(fh.Filename) {
			continue
		}

		filename := sanitizeFilename(fh.Filename)
		relPath := filename
		if folderPath != "" {
			relPath = filepath.Join(folderPath, filename)
		}

		absPath := h.resolveConflict(filepath.Join(h.cfg.MediaRoot, relPath))

		if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
			continue
		}

		file, err := fh.Open()
		if err != nil {
			continue
		}

		dst, err := os.Create(absPath)
		if err != nil {
			_ = file.Close()
			continue
		}

		_, _ = io.Copy(dst, file)
		_ = dst.Close()
		_ = file.Close()
	}

	go func() {
		_ = h.scanSvc.ScanFolder(context.Background(), folderPath)
	}()
	http.Redirect(w, r, "/admin/photos", http.StatusSeeOther)
}

func (h *Handlers) adminUploadFile(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
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

	ctx := r.Context()
	var folderPath string
	if fidStr := r.FormValue("folder_id"); fidStr != "" {
		fid, _ := strconv.Atoi(fidStr)
		_ = h.db.Pool().QueryRow(ctx, "SELECT path FROM folders WHERE id = $1", fid).Scan(&folderPath)
	}

	filename := sanitizeFilename(header.Filename)
	relPath := filename
	if folderPath != "" {
		relPath = filepath.Join(folderPath, filename)
	}

	absPath := h.resolveConflict(filepath.Join(h.cfg.MediaRoot, relPath))

	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	dst, err := os.Create(absPath)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer func() { _ = dst.Close() }()

	if _, err := io.Copy(dst, file); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	go func() {
		_ = h.scanSvc.ScanFolder(context.Background(), folderPath)
	}()
	h.jsonResponse(w, map[string]string{"status": "ok"})
}

func (h *Handlers) adminUploadFinalize(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UploadID string `json:"upload_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	h.uploadsMux.Lock()
	upload, exists := h.uploads[req.UploadID]
	if exists {
		delete(h.uploads, req.UploadID)
	}
	h.uploadsMux.Unlock()

	if !exists {
		http.Error(w, "Upload not found", 404)
		return
	}

	defer func() { _ = os.RemoveAll(upload.TempDir) }()

	ctx := r.Context()
	var folderPath string
	if upload.FolderID != nil {
		_ = h.db.Pool().QueryRow(ctx, "SELECT path FROM folders WHERE id = $1", *upload.FolderID).Scan(&folderPath)
	}

	relPath := upload.Filename
	if folderPath != "" {
		relPath = filepath.Join(folderPath, upload.Filename)
	}

	absPath := h.resolveConflict(filepath.Join(h.cfg.MediaRoot, relPath))

	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	dst, err := os.Create(absPath)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer func() { _ = dst.Close() }()

	for i := 0; i < len(upload.Chunks); i++ {
		chunk, err := os.Open(filepath.Join(upload.TempDir, fmt.Sprintf("chunk_%d", i)))
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_, _ = io.Copy(dst, chunk)
		_ = chunk.Close()
	}

	go func() {
		_ = h.scanSvc.ScanFolder(context.Background(), folderPath)
	}()
	h.jsonResponse(w, map[string]string{"status": "ok"})
}

func (h *Handlers) adminUploadInit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Filename string         `json:"filename"`
		Size     int64          `json:"size"`
		FolderID IntPtrOrString `json:"folder_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	if !isImageFile(req.Filename) {
		http.Error(w, "Invalid file type", 400)
		return
	}

	uploadID := fmt.Sprintf("%d-%s", time.Now().UnixNano(), randString(8))
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
		FolderID:  req.FolderID.V,
		TempDir:   tempDir,
		Chunks:    make(map[int]bool),
		CreatedAt: time.Now(),
	}
	h.uploadsMux.Unlock()

	h.jsonResponse(w, map[string]string{"upload_id": uploadID})
}

func (h *Handlers) adminUploadChunk(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(2 << 20); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	uploadID := r.FormValue("upload_id")
	chunkIndex, _ := strconv.Atoi(r.FormValue("chunk_index"))

	h.uploadsMux.RLock()
	upload, exists := h.uploads[uploadID]
	h.uploadsMux.RUnlock()

	if !exists {
		http.Error(w, "Upload not found", 404)
		return
	}

	file, _, err := r.FormFile("chunk")
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	defer func() { _ = file.Close() }()

	chunkPath := filepath.Join(upload.TempDir, fmt.Sprintf("chunk_%d", chunkIndex))
	dst, err := os.Create(chunkPath)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer func() { _ = dst.Close() }()

	if _, err := io.Copy(dst, file); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	h.uploadsMux.Lock()
	upload.Chunks[chunkIndex] = true
	h.uploadsMux.Unlock()

	h.jsonResponse(w, map[string]string{"status": "ok"})
}

func (h *Handlers) render(w http.ResponseWriter, name string, data map[string]interface{}) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

func (h *Handlers) jsonResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(data)
}

func (h *Handlers) getPhotoByID(ctx context.Context, id int) (*models.Photo, error) {
	var photo models.Photo
	err := h.db.Pool().QueryRow(ctx,
		`SELECT id, folder_id, filename, path, COALESCE(url_path, ''), title, description, note, 
		width, height, size_bytes, blurhash, exif_data, hidden, created_at, taken_at 
		FROM photos WHERE id = $1 AND hidden = false`, id).
		Scan(&photo.ID, &photo.FolderID, &photo.Filename, &photo.Path, &photo.URLPath,
			&photo.Title, &photo.Description, &photo.Note,
			&photo.Width, &photo.Height, &photo.SizeBytes, &photo.Blurhash,
			&photo.ExifData, &photo.Hidden, &photo.CreatedAt, &photo.TakenAt)
	return &photo, err
}

func (h *Handlers) getPhotoByURLPath(ctx context.Context, urlPath string) (*models.Photo, error) {
	var photo models.Photo
	err := h.db.Pool().QueryRow(ctx,
		`SELECT id, folder_id, filename, path, url_path, title, description, note, 
		width, height, size_bytes, blurhash, exif_data, hidden, created_at, taken_at 
		FROM photos WHERE url_path = $1 AND hidden = false`, urlPath).
		Scan(&photo.ID, &photo.FolderID, &photo.Filename, &photo.Path, &photo.URLPath,
			&photo.Title, &photo.Description, &photo.Note,
			&photo.Width, &photo.Height, &photo.SizeBytes, &photo.Blurhash,
			&photo.ExifData, &photo.Hidden, &photo.CreatedAt, &photo.TakenAt)
	return &photo, err
}

func (h *Handlers) getAdjacentPhotoInfo(ctx context.Context, photo *models.Photo) (prevURL, nextURL string, prevID, nextID int) {
	var prev, next struct {
		ID      int
		URLPath string
	}

	sortTime := photo.CreatedAt
	if photo.TakenAt.Valid {
		sortTime = photo.TakenAt.Time
	}

	if photo.FolderID.Valid {
		_ = h.db.Pool().QueryRow(ctx,
			`SELECT id, COALESCE(url_path, '') FROM photos 
			WHERE folder_id = $1 AND hidden = false 
			AND (COALESCE(taken_at, created_at) > $2 OR (COALESCE(taken_at, created_at) = $2 AND id > $3))
			ORDER BY COALESCE(taken_at, created_at) ASC, id ASC LIMIT 1`,
			photo.FolderID.Int64, sortTime, photo.ID).Scan(&prev.ID, &prev.URLPath)

		_ = h.db.Pool().QueryRow(ctx,
			`SELECT id, COALESCE(url_path, '') FROM photos 
			WHERE folder_id = $1 AND hidden = false 
			AND (COALESCE(taken_at, created_at) < $2 OR (COALESCE(taken_at, created_at) = $2 AND id < $3))
			ORDER BY COALESCE(taken_at, created_at) DESC, id DESC LIMIT 1`,
			photo.FolderID.Int64, sortTime, photo.ID).Scan(&next.ID, &next.URLPath)
	} else {
		_ = h.db.Pool().QueryRow(ctx,
			`SELECT id, COALESCE(url_path, '') FROM photos 
			WHERE folder_id IS NULL AND hidden = false 
			AND (COALESCE(taken_at, created_at) > $1 OR (COALESCE(taken_at, created_at) = $1 AND id > $2))
			ORDER BY COALESCE(taken_at, created_at) ASC, id ASC LIMIT 1`,
			sortTime, photo.ID).Scan(&prev.ID, &prev.URLPath)

		_ = h.db.Pool().QueryRow(ctx,
			`SELECT id, COALESCE(url_path, '') FROM photos 
			WHERE folder_id IS NULL AND hidden = false 
			AND (COALESCE(taken_at, created_at) < $1 OR (COALESCE(taken_at, created_at) = $1 AND id < $2))
			ORDER BY COALESCE(taken_at, created_at) DESC, id DESC LIMIT 1`,
			sortTime, photo.ID).Scan(&next.ID, &next.URLPath)
	}

	if prev.ID > 0 {
		prevID = prev.ID
		if prev.URLPath != "" {
			prevURL = "/p/" + prev.URLPath
		} else {
			prevURL = fmt.Sprintf("/photo/%d", prev.ID)
		}
	}
	if next.ID > 0 {
		nextID = next.ID
		if next.URLPath != "" {
			nextURL = "/p/" + next.URLPath
		} else {
			nextURL = fmt.Sprintf("/photo/%d", next.ID)
		}
	}
	return
}

func (h *Handlers) getPhotoPosition(ctx context.Context, photo *models.Photo) (position, total int) {
	_ = h.db.Pool().QueryRow(ctx,
		`SELECT COUNT(*) FROM photos WHERE folder_id IS NOT DISTINCT FROM $1 AND hidden = false`,
		photo.FolderID).Scan(&total)

	_ = h.db.Pool().QueryRow(ctx,
		`SELECT COUNT(*) + 1 FROM photos 
		WHERE folder_id IS NOT DISTINCT FROM $1 AND hidden = false 
		AND (COALESCE(taken_at, created_at), id) > (COALESCE($2, $3), $4)`,
		photo.FolderID, photo.TakenAt, photo.CreatedAt, photo.ID).Scan(&position)

	return
}

func (h *Handlers) getPhotoBreadcrumbs(ctx context.Context, photo *models.Photo) []models.Folder {
	if !photo.FolderID.Valid {
		return nil
	}

	var folder models.Folder
	if err := h.db.Pool().QueryRow(ctx, "SELECT id, parent_id, name, path FROM folders WHERE id = $1",
		photo.FolderID.Int64).Scan(&folder.ID, &folder.ParentID, &folder.Name, &folder.Path); err != nil {
		return nil
	}
	return h.getBreadcrumbs(ctx, &folder)
}

func (h *Handlers) getRootFolders(ctx context.Context) ([]models.Folder, error) {
	return h.getFoldersWithCounts(ctx, "parent_id IS NULL")
}

func (h *Handlers) getSubfolders(ctx context.Context, parentID int) ([]models.Folder, error) {
	return h.getFoldersWithCounts(ctx, fmt.Sprintf("parent_id = %d", parentID))
}

func (h *Handlers) getFoldersWithCounts(ctx context.Context, where string) ([]models.Folder, error) {
	query := fmt.Sprintf(`
		SELECT f.id, f.parent_id, f.name, f.path, f.cover_photo_id, f.created_at,
			(SELECT COUNT(*) FROM photos WHERE folder_id = f.id AND hidden = false) as photo_count,
			(SELECT COUNT(*) FROM folders WHERE parent_id = f.id) as subfolder_count,
			(SELECT COALESCE(SUM(size_bytes), 0) FROM photos WHERE folder_id = f.id AND hidden = false) as total_size,
			(SELECT ARRAY(
				SELECT p.id FROM photos p WHERE p.folder_id = f.id AND p.hidden = false 
				ORDER BY COALESCE(p.taken_at, p.created_at) DESC, p.id DESC LIMIT 4
			)) as preview_ids
		FROM folders f WHERE %s ORDER BY f.created_at DESC`, where)

	rows, err := h.db.Pool().Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var folders []models.Folder
	for rows.Next() {
		var f models.Folder
		var previewIDs []int64
		if err := rows.Scan(&f.ID, &f.ParentID, &f.Name, &f.Path, &f.CoverPhotoID, &f.CreatedAt,
			&f.PhotoCount, &f.SubfolderCount, &f.TotalSize, &previewIDs); err != nil {
			continue
		}

		for _, pid := range previewIDs {
			f.PreviewURLs = append(f.PreviewURLs, fmt.Sprintf("/thumb/small/%d", pid))
		}
		if len(f.PreviewURLs) > 0 {
			f.CoverURL = f.PreviewURLs[0]
		}
		folders = append(folders, f)
	}
	return folders, nil
}

func (h *Handlers) getRootPhotos(ctx context.Context) ([]models.Photo, error) {
	return h.getPhotos(ctx, "folder_id IS NULL AND hidden = false")
}

func (h *Handlers) getFolderPhotos(ctx context.Context, folderID int) ([]models.Photo, error) {
	return h.getPhotos(ctx, fmt.Sprintf("folder_id = %d AND hidden = false", folderID))
}

func (h *Handlers) getPhotos(ctx context.Context, where string) ([]models.Photo, error) {
	query := fmt.Sprintf(`
		SELECT id, folder_id, filename, path, COALESCE(url_path, ''), title, width, height, blurhash, size_bytes, taken_at, created_at
		FROM photos WHERE %s ORDER BY COALESCE(taken_at, created_at) DESC, id DESC`, where)

	rows, err := h.db.Pool().Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var photos []models.Photo
	for rows.Next() {
		var p models.Photo
		if err := rows.Scan(&p.ID, &p.FolderID, &p.Filename, &p.Path, &p.URLPath, &p.Title, &p.Width, &p.Height, &p.Blurhash, &p.SizeBytes, &p.TakenAt, &p.CreatedAt); err != nil {
			continue
		}
		photos = append(photos, p)
	}
	return photos, nil
}

func (h *Handlers) getAllFolders(ctx context.Context) ([]models.Folder, error) {
	rows, err := h.db.Pool().Query(ctx, "SELECT id, parent_id, name, path FROM folders ORDER BY path")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var folders []models.Folder
	for rows.Next() {
		var f models.Folder
		if err := rows.Scan(&f.ID, &f.ParentID, &f.Name, &f.Path); err != nil {
			continue
		}
		folders = append(folders, f)
	}
	return folders, nil
}

func (h *Handlers) getFolderTree(ctx context.Context) ([]models.Folder, error) {
	query := `
		WITH RECURSIVE folder_tree AS (
			SELECT id, parent_id, name, path, cover_photo_id, created_at, 0 as depth
			FROM folders WHERE parent_id IS NULL
			UNION ALL
			SELECT f.id, f.parent_id, f.name, f.path, f.cover_photo_id, f.created_at, ft.depth + 1
			FROM folders f INNER JOIN folder_tree ft ON f.parent_id = ft.id
		)
		SELECT ft.id, ft.parent_id, ft.name, ft.path, ft.cover_photo_id, ft.created_at, ft.depth,
			(SELECT COUNT(*) FROM photos WHERE folder_id = ft.id AND hidden = false),
			(SELECT COUNT(*) FROM folders WHERE parent_id = ft.id),
			(SELECT COALESCE(SUM(size_bytes), 0) FROM photos WHERE folder_id = ft.id AND hidden = false),
			COALESCE(ft.cover_photo_id, (SELECT p.id FROM photos p WHERE p.folder_id = ft.id AND p.hidden = false 
				ORDER BY COALESCE(p.taken_at, p.created_at) DESC, p.id DESC LIMIT 1))
		FROM folder_tree ft ORDER BY ft.path`

	rows, err := h.db.Pool().Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var folders []models.Folder
	for rows.Next() {
		var f models.Folder
		var firstPhotoID sql.NullInt64
		if err := rows.Scan(&f.ID, &f.ParentID, &f.Name, &f.Path, &f.CoverPhotoID, &f.CreatedAt, &f.Depth,
			&f.PhotoCount, &f.SubfolderCount, &f.TotalSize, &firstPhotoID); err != nil {
			continue
		}
		if firstPhotoID.Valid {
			f.CoverURL = fmt.Sprintf("/thumb/small/%d", firstPhotoID.Int64)
		}
		f.HasChildren = f.SubfolderCount > 0
		folders = append(folders, f)
	}
	return folders, nil
}

func (h *Handlers) getBreadcrumbs(ctx context.Context, folder *models.Folder) []models.Folder {
	var breadcrumbs []models.Folder
	current := folder

	for current != nil {
		breadcrumbs = append([]models.Folder{*current}, breadcrumbs...)
		if !current.ParentID.Valid {
			break
		}
		var parent models.Folder
		if err := h.db.Pool().QueryRow(ctx, "SELECT id, parent_id, name, path FROM folders WHERE id = $1",
			current.ParentID.Int64).Scan(&parent.ID, &parent.ParentID, &parent.Name, &parent.Path); err != nil {
			break
		}
		current = &parent
	}
	return breadcrumbs
}

func (h *Handlers) isPathSafe(path string) bool {
	cleaned := filepath.Clean(path)
	if strings.Contains(cleaned, "..") {
		return false
	}
	return strings.HasPrefix(filepath.Join(h.cfg.MediaRoot, cleaned), h.cfg.MediaRoot)
}

func (h *Handlers) resolveConflict(path string) string {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path
	}

	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)

	for i := 1; i < 10000; i++ {
		newPath := fmt.Sprintf("%s_%d%s", base, i, ext)
		if _, err := os.Stat(newPath); os.IsNotExist(err) {
			return newPath
		}
	}
	return fmt.Sprintf("%s_%d%s", base, time.Now().UnixNano(), ext)
}

func formatSize(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func sanitizeFilename(name string) string {
	name = filepath.Base(name)
	name = strings.ReplaceAll(name, "..", "")
	name = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|' {
			return '_'
		}
		return r
	}, name)
	if name == "" || name == "." {
		name = "unnamed"
	}
	return name
}

func randString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:n]
}

func isImageFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".jpg" || ext == ".jpeg" || ext == ".png"
}

func (h *Handlers) apiListFolders(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	parentIDStr := r.URL.Query().Get("parent_id")

	var where string
	var args []interface{}

	if parentIDStr == "" {
		where = "parent_id IS NULL"
	} else if parentIDStr == "root" {
		where = "parent_id IS NULL"
	} else {
		pid, err := strconv.Atoi(parentIDStr)
		if err != nil {
			http.Error(w, "invalid parent_id", 400)
			return
		}
		where = "parent_id = $1"
		args = append(args, pid)
	}

	query := fmt.Sprintf(`
		SELECT f.id, f.parent_id, f.name, f.path, f.cover_photo_id, f.created_at,
			(SELECT COUNT(*) FROM photos WHERE folder_id = f.id AND hidden = false) as photo_count,
			(SELECT COUNT(*) FROM folders WHERE parent_id = f.id) as subfolder_count,
			(SELECT COALESCE(SUM(size_bytes), 0) FROM photos WHERE folder_id = f.id AND hidden = false) as total_size
		FROM folders f WHERE %s ORDER BY f.name`, where)

	rows, err := h.db.Pool().Query(ctx, query, args...)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()

	type folderJSON struct {
		ID             int    `json:"id"`
		ParentID       *int   `json:"parent_id"`
		Name           string `json:"name"`
		Path           string `json:"path"`
		CoverPhotoID   *int   `json:"cover_photo_id"`
		CreatedAt      string `json:"created_at"`
		PhotoCount     int    `json:"photo_count"`
		SubfolderCount int    `json:"subfolder_count"`
		TotalSize      int64  `json:"total_size"`
	}

	var folders []folderJSON
	for rows.Next() {
		var f folderJSON
		var parentID sql.NullInt64
		var coverPhotoID sql.NullInt64
		var createdAt time.Time

		if err := rows.Scan(&f.ID, &parentID, &f.Name, &f.Path, &coverPhotoID, &createdAt,
			&f.PhotoCount, &f.SubfolderCount, &f.TotalSize); err != nil {
			continue
		}

		if parentID.Valid {
			pid := int(parentID.Int64)
			f.ParentID = &pid
		}
		if coverPhotoID.Valid {
			cid := int(coverPhotoID.Int64)
			f.CoverPhotoID = &cid
		}
		f.CreatedAt = createdAt.Format(time.RFC3339)
		folders = append(folders, f)
	}

	if folders == nil {
		folders = []folderJSON{}
	}

	h.jsonResponse(w, map[string]interface{}{
		"folders": folders,
	})
}

func (h *Handlers) apiGetFolder(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}

	ctx := r.Context()

	var parentID sql.NullInt64
	var coverPhotoID sql.NullInt64
	var name, path string
	var createdAt time.Time
	var photoCount, subfolderCount int
	var totalSize int64

	err = h.db.Pool().QueryRow(ctx, `
		SELECT f.id, f.parent_id, f.name, f.path, f.cover_photo_id, f.created_at,
			(SELECT COUNT(*) FROM photos WHERE folder_id = f.id AND hidden = false),
			(SELECT COUNT(*) FROM folders WHERE parent_id = f.id),
			(SELECT COALESCE(SUM(size_bytes), 0) FROM photos WHERE folder_id = f.id AND hidden = false)
		FROM folders f WHERE f.id = $1`, id).
		Scan(&id, &parentID, &name, &path, &coverPhotoID, &createdAt,
			&photoCount, &subfolderCount, &totalSize)

	if err != nil {
		http.NotFound(w, r)
		return
	}

	folder := map[string]interface{}{
		"id":              id,
		"parent_id":       nil,
		"name":            name,
		"path":            path,
		"cover_photo_id":  nil,
		"created_at":      createdAt.Format(time.RFC3339),
		"photo_count":     photoCount,
		"subfolder_count": subfolderCount,
		"total_size":      totalSize,
	}

	if parentID.Valid {
		folder["parent_id"] = int(parentID.Int64)
	}
	if coverPhotoID.Valid {
		folder["cover_photo_id"] = int(coverPhotoID.Int64)
	}

	h.jsonResponse(w, folder)
}

func (h *Handlers) apiListPhotos(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}

	perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	if perPage < 1 || perPage > 100 {
		perPage = 50
	}

	offset := (page - 1) * perPage
	folderFilter := r.URL.Query().Get("folder_id")

	query := `SELECT id, folder_id, filename, path, COALESCE(url_path, ''), title, description,
		width, height, size_bytes, blurhash, hidden, created_at, taken_at
		FROM photos WHERE hidden = false`
	countQuery := "SELECT COUNT(*) FROM photos WHERE hidden = false"

	var args []interface{}
	argIdx := 1

	if folderFilter == "root" {
		query += " AND folder_id IS NULL"
		countQuery += " AND folder_id IS NULL"
	} else if folderFilter != "" {
		fid, err := strconv.Atoi(folderFilter)
		if err != nil {
			http.Error(w, "invalid folder_id", 400)
			return
		}
		query += fmt.Sprintf(" AND folder_id = $%d", argIdx)
		countQuery += fmt.Sprintf(" AND folder_id = $%d", argIdx)
		args = append(args, fid)
		argIdx++
	}

	var totalCount int
	_ = h.db.Pool().QueryRow(ctx, countQuery, args...).Scan(&totalCount)

	query += fmt.Sprintf(" ORDER BY COALESCE(taken_at, created_at) DESC, id DESC LIMIT $%d OFFSET $%d", argIdx, argIdx+1)
	args = append(args, perPage, offset)

	rows, err := h.db.Pool().Query(ctx, query, args...)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()

	type photoJSON struct {
		ID          int     `json:"id"`
		FolderID    *int    `json:"folder_id"`
		Filename    string  `json:"filename"`
		Path        string  `json:"path"`
		URL         string  `json:"url"`
		Title       *string `json:"title"`
		Description *string `json:"description"`
		Width       int     `json:"width"`
		Height      int     `json:"height"`
		SizeBytes   int64   `json:"size_bytes"`
		Blurhash    *string `json:"blurhash"`
		CreatedAt   string  `json:"created_at"`
		TakenAt     *string `json:"taken_at"`
		Thumbnails  struct {
			Small  string `json:"small"`
			Medium string `json:"medium"`
			Large  string `json:"large"`
		} `json:"thumbnails"`
	}

	var photos []photoJSON
	for rows.Next() {
		var p photoJSON
		var folderID sql.NullInt64
		var urlPath string
		var title, description, blurhash sql.NullString
		var createdAt time.Time
		var takenAt sql.NullTime
		var hidden bool

		if err := rows.Scan(&p.ID, &folderID, &p.Filename, &p.Path, &urlPath, &title, &description,
			&p.Width, &p.Height, &p.SizeBytes, &blurhash, &hidden, &createdAt, &takenAt); err != nil {
			continue
		}

		if folderID.Valid {
			fid := int(folderID.Int64)
			p.FolderID = &fid
		}
		if urlPath != "" {
			p.URL = "/p/" + urlPath
		} else {
			p.URL = fmt.Sprintf("/photo/%d", p.ID)
		}
		if title.Valid {
			p.Title = &title.String
		}
		if description.Valid {
			p.Description = &description.String
		}
		if blurhash.Valid {
			p.Blurhash = &blurhash.String
		}
		p.CreatedAt = createdAt.Format(time.RFC3339)
		if takenAt.Valid {
			t := takenAt.Time.Format(time.RFC3339)
			p.TakenAt = &t
		}
		p.Thumbnails.Small = fmt.Sprintf("/thumb/small/%d", p.ID)
		p.Thumbnails.Medium = fmt.Sprintf("/thumb/medium/%d", p.ID)
		p.Thumbnails.Large = fmt.Sprintf("/thumb/large/%d", p.ID)

		photos = append(photos, p)
	}

	if photos == nil {
		photos = []photoJSON{}
	}

	h.jsonResponse(w, map[string]interface{}{
		"photos":   photos,
		"page":     page,
		"per_page": perPage,
		"total":    totalCount,
		"pages":    (totalCount + perPage - 1) / perPage,
	})
}

func (h *Handlers) apiGetPhoto(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}

	ctx := r.Context()

	var folderID sql.NullInt64
	var filename, path, urlPath string
	var title, description, note, blurhash sql.NullString
	var width, height int
	var sizeBytes int64
	var exifData json.RawMessage
	var hidden bool
	var createdAt time.Time
	var takenAt sql.NullTime

	err = h.db.Pool().QueryRow(ctx, `
		SELECT id, folder_id, filename, path, COALESCE(url_path, ''), title, description, note,
			width, height, size_bytes, blurhash, exif_data, hidden, created_at, taken_at
		FROM photos WHERE id = $1 AND hidden = false`, id).
		Scan(&id, &folderID, &filename, &path, &urlPath, &title, &description, &note,
			&width, &height, &sizeBytes, &blurhash, &exifData, &hidden, &createdAt, &takenAt)

	if err != nil {
		http.NotFound(w, r)
		return
	}

	photo := map[string]interface{}{
		"id":          id,
		"folder_id":   nil,
		"filename":    filename,
		"path":        path,
		"url":         fmt.Sprintf("/photo/%d", id),
		"title":       nil,
		"description": nil,
		"note":        nil,
		"width":       width,
		"height":      height,
		"size_bytes":  sizeBytes,
		"blurhash":    nil,
		"created_at":  createdAt.Format(time.RFC3339),
		"taken_at":    nil,
		"thumbnails": map[string]string{
			"small":  fmt.Sprintf("/thumb/small/%d", id),
			"medium": fmt.Sprintf("/thumb/medium/%d", id),
			"large":  fmt.Sprintf("/thumb/large/%d", id),
		},
		"original": fmt.Sprintf("/original/%d", id),
	}

	if folderID.Valid {
		photo["folder_id"] = int(folderID.Int64)
	}
	if urlPath != "" {
		photo["url"] = "/p/" + urlPath
	}
	if title.Valid {
		photo["title"] = title.String
	}
	if description.Valid {
		photo["description"] = description.String
	}
	if note.Valid {
		photo["note"] = note.String
	}
	if blurhash.Valid {
		photo["blurhash"] = blurhash.String
	}
	if takenAt.Valid {
		photo["taken_at"] = takenAt.Time.Format(time.RFC3339)
	}
	if exifData != nil {
		var exif map[string]interface{}
		if json.Unmarshal(exifData, &exif) == nil {
			photo["exif"] = exif
		}
	}

	h.jsonResponse(w, photo)
}
