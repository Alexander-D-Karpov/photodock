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
		"add":     func(a, b int) int { return a + b },
		"sub":     func(a, b int) int { return a - b },
		"int64":   func(i int) int64 { return int64(i) },
		"urlpath": escapeURLPath,
		"iterate": func(n int) []int {
			result := make([]int, n)
			for i := range result {
				result[i] = i
			}
			return result
		},
	}

	tmplFS, _ := fs.Sub(webFS, "web/templates")
	tmpl := template.New("").Funcs(funcMap)

	fs.WalkDir(tmplFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".html") {
			return err
		}
		content, err := fs.ReadFile(tmplFS, path)
		if err != nil {
			return err
		}
		tmpl.New(path).Parse(string(content))
		return nil
	})

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

	// null
	if s == "null" {
		x.V = nil
		return nil
	}

	// "123" / "" / "null"
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

	// 123
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
	folders, _ := h.getRootFolders(ctx)
	photos, _ := h.getRootPhotos(ctx)

	var photoCount, folderCount int
	var totalSize int64
	h.db.Pool().QueryRow(ctx, "SELECT COUNT(*) FROM photos WHERE hidden = false").Scan(&photoCount)
	h.db.Pool().QueryRow(ctx, "SELECT COUNT(*) FROM folders").Scan(&folderCount)
	h.db.Pool().QueryRow(ctx, "SELECT COALESCE(SUM(size_bytes), 0) FROM photos WHERE hidden = false").Scan(&totalSize)

	h.render(w, "public/index.html", map[string]interface{}{
		"Folders":     folders,
		"Photos":      photos,
		"Title":       "Index",
		"PhotoCount":  photoCount,
		"FolderCount": folderCount,
		"TotalSize":   totalSize,
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

func (h *Handlers) publicPhotoByPath(w http.ResponseWriter, r *http.Request) {
	urlPath := r.PathValue("path")
	if urlPath == "" {
		http.NotFound(w, r)
		return
	}

	photo, err := h.getPhotoByURLPath(r.Context(), urlPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	h.renderPhoto(w, r, photo)
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
		json.Unmarshal(photo.ExifData, &exifInfo)
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

	if size != "small" && size != "medium" {
		http.NotFound(w, r)
		return
	}

	var path string
	if err := h.db.Pool().QueryRow(r.Context(), "SELECT path FROM photos WHERE id = $1", id).Scan(&path); err != nil {
		http.NotFound(w, r)
		return
	}

	thumbPath, err := h.thumbSvc.GetThumbnailPath(path, size)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	w.Header().Set("Cache-Control", "public, max-age=31536000")
	http.ServeFile(w, r, thumbPath)
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

	w.Header().Set("Cache-Control", "public, max-age=31536000")
	http.ServeFile(w, r, filepath.Join(h.cfg.MediaRoot, path))
}

func (h *Handlers) servePlaceholder(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.PathValue("id"))

	var path, blurhash string
	if err := h.db.Pool().QueryRow(r.Context(), "SELECT path, COALESCE(blurhash, '') FROM photos WHERE id = $1", id).Scan(&path, &blurhash); err != nil {
		http.NotFound(w, r)
		return
	}

	placeholderPath, err := h.thumbSvc.GetPlaceholderPath(path, blurhash)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	w.Header().Set("Cache-Control", "public, max-age=31536000")
	http.ServeFile(w, r, placeholderPath)
}

func (h *Handlers) adminDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var photoCount, folderCount, hiddenCount int
	var totalSize int64

	h.db.Pool().QueryRow(ctx, "SELECT COUNT(*) FROM photos").Scan(&photoCount)
	h.db.Pool().QueryRow(ctx, "SELECT COUNT(*) FROM folders").Scan(&folderCount)
	h.db.Pool().QueryRow(ctx, "SELECT COUNT(*) FROM photos WHERE hidden = true").Scan(&hiddenCount)
	h.db.Pool().QueryRow(ctx, "SELECT COALESCE(SUM(size_bytes), 0) FROM photos").Scan(&totalSize)

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
		h.db.Pool().QueryRow(ctx, "SELECT path FROM folders WHERE id = $1", pid).Scan(&parentPath)
	}

	path := name
	if parentPath != "" {
		path = filepath.Join(parentPath, name)
	}

	if err := os.MkdirAll(filepath.Join(h.cfg.MediaRoot, path), 0755); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	h.db.Pool().Exec(ctx,
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

	h.db.Pool().Exec(r.Context(), "UPDATE folders SET name = $1, updated_at = NOW() WHERE id = $2", name, id)
	http.Redirect(w, r, "/admin/folders", http.StatusSeeOther)
}

func (h *Handlers) adminDeleteFolder(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.PathValue("id"))
	h.db.Pool().Exec(r.Context(), "DELETE FROM folders WHERE id = $1", id)
	w.WriteHeader(http.StatusOK)
}

func (h *Handlers) adminSetCover(w http.ResponseWriter, r *http.Request) {
	folderID, _ := strconv.Atoi(r.PathValue("id"))

	var photoID *int
	if pidStr := r.FormValue("photo_id"); pidStr != "" {
		pid, _ := strconv.Atoi(pidStr)
		photoID = &pid
	}

	h.db.Pool().Exec(r.Context(),
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
	h.db.Pool().QueryRow(ctx, countQuery, args...).Scan(&totalCount)

	query += fmt.Sprintf(" ORDER BY COALESCE(taken_at, created_at) DESC, id DESC LIMIT $%d OFFSET $%d", argIdx, argIdx+1)
	args = append(args, perPage, offset)

	rows, _ := h.db.Pool().Query(ctx, query, args...)
	defer rows.Close()

	var photos []models.Photo
	for rows.Next() {
		var p models.Photo
		rows.Scan(&p.ID, &p.FolderID, &p.Filename, &p.Path, &p.Title, &p.Hidden, &p.Width, &p.Height)
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
		json.Unmarshal(photo.ExifData, &exifInfo)
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

	h.db.Pool().Exec(r.Context(),
		`UPDATE photos SET title = NULLIF($1, ''), description = NULLIF($2, ''), 
		note = NULLIF($3, ''), folder_id = $4, updated_at = NOW() WHERE id = $5`,
		r.FormValue("title"), r.FormValue("description"), r.FormValue("note"), folderID, id)

	http.Redirect(w, r, fmt.Sprintf("/admin/photos/%d", id), http.StatusSeeOther)
}

func (h *Handlers) adminDeletePhoto(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.PathValue("id"))
	ctx := r.Context()

	var path string
	h.db.Pool().QueryRow(ctx, "SELECT path FROM photos WHERE id = $1", id).Scan(&path)
	h.db.Pool().Exec(ctx, "DELETE FROM photos WHERE id = $1", id)

	if path != "" {
		h.thumbSvc.DeleteThumbnails(path)
		os.Remove(filepath.Join(h.cfg.MediaRoot, path))
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handlers) adminToggleHide(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.PathValue("id"))
	h.db.Pool().Exec(r.Context(), "UPDATE photos SET hidden = NOT hidden, updated_at = NOW() WHERE id = $1", id)
	w.WriteHeader(http.StatusOK)
}

func (h *Handlers) adminMovePhoto(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.PathValue("id"))

	var folderID *int
	if fidStr := r.FormValue("folder_id"); fidStr != "" && fidStr != "" {
		fid, _ := strconv.Atoi(fidStr)
		if fid > 0 {
			folderID = &fid
		}
	}

	h.db.Pool().Exec(r.Context(), "UPDATE photos SET folder_id = $1, updated_at = NOW() WHERE id = $2", folderID, id)
	w.WriteHeader(http.StatusOK)
}

func (h *Handlers) adminScan(w http.ResponseWriter, r *http.Request) {
	go h.scanSvc.ScanAll(context.Background())
	h.jsonResponse(w, map[string]string{"status": "started"})
}

func (h *Handlers) adminScanFolder(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.PathValue("id"))

	var path string
	if err := h.db.Pool().QueryRow(r.Context(), "SELECT path FROM folders WHERE id = $1", id).Scan(&path); err != nil {
		http.NotFound(w, r)
		return
	}

	go h.scanSvc.ScanFolder(context.Background(), path)
	h.jsonResponse(w, map[string]string{"status": "started"})
}

func (h *Handlers) adminClean(w http.ResponseWriter, r *http.Request) {
	go h.scanSvc.CleanOrphans(context.Background())
	h.jsonResponse(w, map[string]string{"status": "started"})
}

func (h *Handlers) adminRegenerateURLs(w http.ResponseWriter, r *http.Request) {
	go h.scanSvc.RegenerateURLPaths(context.Background())
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
		h.db.Pool().QueryRow(ctx, "SELECT path FROM folders WHERE id = $1", fid).Scan(&folderPath)
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

		// Ensure destination directory exists
		if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
			continue
		}

		file, err := fh.Open()
		if err != nil {
			continue
		}

		dst, err := os.Create(absPath)
		if err != nil {
			file.Close()
			continue
		}

		_, _ = io.Copy(dst, file)
		dst.Close()
		file.Close()
	}

	go h.scanSvc.ScanFolder(context.Background(), folderPath)
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
	defer file.Close()

	if !isImageFile(header.Filename) {
		http.Error(w, "Invalid file type", 400)
		return
	}

	ctx := r.Context()
	var folderPath string
	if fidStr := r.FormValue("folder_id"); fidStr != "" {
		fid, _ := strconv.Atoi(fidStr)
		h.db.Pool().QueryRow(ctx, "SELECT path FROM folders WHERE id = $1", fid).Scan(&folderPath)
	}

	filename := sanitizeFilename(header.Filename)
	relPath := filename
	if folderPath != "" {
		relPath = filepath.Join(folderPath, filename)
	}

	absPath := h.resolveConflict(filepath.Join(h.cfg.MediaRoot, relPath))

	// Ensure destination directory exists
	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	dst, err := os.Create(absPath)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	go h.scanSvc.ScanFolder(context.Background(), folderPath)
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

	defer os.RemoveAll(upload.TempDir)

	ctx := r.Context()
	var folderPath string
	if upload.FolderID != nil {
		h.db.Pool().QueryRow(ctx, "SELECT path FROM folders WHERE id = $1", *upload.FolderID).Scan(&folderPath)
	}

	relPath := upload.Filename
	if folderPath != "" {
		relPath = filepath.Join(folderPath, upload.Filename)
	}

	absPath := h.resolveConflict(filepath.Join(h.cfg.MediaRoot, relPath))

	// Ensure destination directory exists
	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	dst, err := os.Create(absPath)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer dst.Close()

	for i := 0; i < len(upload.Chunks); i++ {
		chunk, err := os.Open(filepath.Join(upload.TempDir, fmt.Sprintf("chunk_%d", i)))
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_, _ = io.Copy(dst, chunk)
		chunk.Close()
	}

	go h.scanSvc.ScanFolder(context.Background(), folderPath)
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
	defer file.Close()

	chunkPath := filepath.Join(upload.TempDir, fmt.Sprintf("chunk_%d", chunkIndex))
	dst, err := os.Create(chunkPath)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer dst.Close()

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
	json.NewEncoder(w).Encode(data)
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
		h.db.Pool().QueryRow(ctx,
			`SELECT id, COALESCE(url_path, '') FROM photos 
			WHERE folder_id = $1 AND hidden = false 
			AND (COALESCE(taken_at, created_at) > $2 OR (COALESCE(taken_at, created_at) = $2 AND id > $3))
			ORDER BY COALESCE(taken_at, created_at) ASC, id ASC LIMIT 1`,
			photo.FolderID.Int64, sortTime, photo.ID).Scan(&prev.ID, &prev.URLPath)

		h.db.Pool().QueryRow(ctx,
			`SELECT id, COALESCE(url_path, '') FROM photos 
			WHERE folder_id = $1 AND hidden = false 
			AND (COALESCE(taken_at, created_at) < $2 OR (COALESCE(taken_at, created_at) = $2 AND id < $3))
			ORDER BY COALESCE(taken_at, created_at) DESC, id DESC LIMIT 1`,
			photo.FolderID.Int64, sortTime, photo.ID).Scan(&next.ID, &next.URLPath)
	} else {
		h.db.Pool().QueryRow(ctx,
			`SELECT id, COALESCE(url_path, '') FROM photos 
			WHERE folder_id IS NULL AND hidden = false 
			AND (COALESCE(taken_at, created_at) > $1 OR (COALESCE(taken_at, created_at) = $1 AND id > $2))
			ORDER BY COALESCE(taken_at, created_at) ASC, id ASC LIMIT 1`,
			sortTime, photo.ID).Scan(&prev.ID, &prev.URLPath)

		h.db.Pool().QueryRow(ctx,
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
	h.db.Pool().QueryRow(ctx,
		`SELECT COUNT(*) FROM photos WHERE folder_id IS NOT DISTINCT FROM $1 AND hidden = false`,
		photo.FolderID).Scan(&total)

	h.db.Pool().QueryRow(ctx,
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
			COALESCE(f.cover_photo_id, (SELECT p.id FROM photos p WHERE p.folder_id = f.id AND p.hidden = false 
				ORDER BY COALESCE(p.taken_at, p.created_at) DESC, p.id DESC LIMIT 1)) as first_photo_id
		FROM folders f WHERE %s ORDER BY f.created_at DESC`, where)

	rows, err := h.db.Pool().Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var folders []models.Folder
	for rows.Next() {
		var f models.Folder
		var firstPhotoID sql.NullInt64
		rows.Scan(&f.ID, &f.ParentID, &f.Name, &f.Path, &f.CoverPhotoID, &f.CreatedAt,
			&f.PhotoCount, &f.SubfolderCount, &f.TotalSize, &firstPhotoID)
		if firstPhotoID.Valid {
			f.CoverURL = fmt.Sprintf("/thumb/small/%d", firstPhotoID.Int64)
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
		rows.Scan(&p.ID, &p.FolderID, &p.Filename, &p.Path, &p.URLPath, &p.Title, &p.Width, &p.Height, &p.Blurhash, &p.SizeBytes, &p.TakenAt, &p.CreatedAt)
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
		rows.Scan(&f.ID, &f.ParentID, &f.Name, &f.Path)
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
		rows.Scan(&f.ID, &f.ParentID, &f.Name, &f.Path, &f.CoverPhotoID, &f.CreatedAt, &f.Depth,
			&f.PhotoCount, &f.SubfolderCount, &f.TotalSize, &firstPhotoID)
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
	rand.Read(b)
	return hex.EncodeToString(b)[:n]
}

func isImageFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".jpg" || ext == ".jpeg" || ext == ".png"
}
