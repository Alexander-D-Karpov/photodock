package main

import (
	"bytes"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Alexander-D-Karpov/photodock/internal/config"
	"github.com/Alexander-D-Karpov/photodock/internal/develop"
	"github.com/Alexander-D-Karpov/photodock/internal/handlers"
	"github.com/Alexander-D-Karpov/photodock/internal/models"
)

// newTestServer builds a route mux backed by the real embedded FS. db/thumb/scan
// are nil, so only DB-free routes (favicon, static) may be exercised.
func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	h := handlers.New(nil, &config.Config{}, nil, nil, nil, webFS)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return mux
}

func TestFaviconRoute(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestServer(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/favicon.ico", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /favicon.ico status = %d; want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/webp" {
		t.Errorf("Content-Type = %q; want image/webp", ct)
	}
	if rec.Body.Len() == 0 {
		t.Error("favicon body is empty")
	}
}

func TestStaticServesEmbeddedAsset(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestServer(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/js/gallery.js", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /static/js/gallery.js status = %d; want 200", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Error("static asset body is empty")
	}
}

// TestEmbeddedTemplatesParse parses every embedded template with the real func
// map, catching template syntax errors (which `go build` does not) at test time.
func TestEmbeddedTemplatesParse(t *testing.T) {
	tmpl, err := handlers.LoadTemplates(webFS)
	if err != nil {
		t.Fatalf("LoadTemplates: %v", err)
	}
	for _, name := range []string{"public/index.html", "public/folder.html", "public/photo.html", "admin/develop.html"} {
		if tmpl.Lookup(name) == nil {
			t.Errorf("template %q not parsed/registered", name)
		}
	}
}

// TestEmbeddedStaticAssetsPresent guards the assets referenced by templates and
// the favicon route, so a missing embed fails CI rather than at runtime.
func TestEmbeddedStaticAssetsPresent(t *testing.T) {
	staticFS, err := fs.Sub(webFS, "web/static")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"js/gallery.js", "js/index.js", "css/public.css", "icon.webp",
		"css/develop.css", "js/develop/main.js", "js/develop/api.js", "js/develop/panels.js",
		"js/develop/preview.js", "js/develop/crop.js", "js/develop/curve.js",
		"js/develop/histogram.js", "js/develop/slider.js", "js/develop/schema.js",
		"js/develop/recipe.js", "js/develop/shortcuts.js", "js/develop/zoom.js",
		"js/develop/wheel.js", "js/develop/dialogs.js", "js/develop/presets.js", "js/develop/export.js",
		"js/develop/masks.js", "js/develop/perf.js", "js/develop-batch.js",
	} {
		if _, err := fs.Stat(staticFS, name); err != nil {
			t.Errorf("missing embedded static asset %q: %v", name, err)
		}
	}
}

// TestTemplatesExecuteWithEditHash executes the pages that build image URLs
// from models.Photo. html/template resolves fields only at execution time, so
// parsing alone would not catch a {{thumb ... .EditHash}} on the wrong type.
func TestTemplatesExecuteWithEditHash(t *testing.T) {
	tmpl, err := handlers.LoadTemplates(webFS)
	if err != nil {
		t.Fatalf("LoadTemplates: %v", err)
	}
	const hash = "0123456789abcdef"
	photos := []models.Photo{
		{ID: 1, Filename: "a.jpg", Width: 300, Height: 200, EditHash: hash},
		{ID: 2, Filename: "b.arw", Width: 200, Height: 300},
	}
	folder := models.Folder{ID: 7, Name: "f", Path: "f"}
	pages := map[string]map[string]interface{}{
		"public/index.html":  {"Photos": photos, "Folders": []models.Folder{folder}, "TotalSize": int64(0)},
		"public/folder.html": {"Photos": photos, "Folder": folder, "Subfolders": []models.Folder{folder}, "Breadcrumbs": []models.Folder{}, "TotalSize": int64(0)},
		"public/photo.html": {"Photo": &photos[0], "BaseURL": "https://example.test", "PrevOriginal": "/original/2?v=x",
			"Develop": develop.Summarize(develop.Recipe{Tone: develop.Tone{Exposure: 0.5}, Lens: develop.Lens{Enabled: true}})},
		"admin/photos.html":      {"Photos": photos, "Folders": []models.Folder{folder}, "TotalPages": 1, "CurrentPage": 1},
		"admin/folder_edit.html": {"Photos": photos, "Folder": folder, "AllFolders": []models.Folder{folder}},
		"admin/photo_edit.html":  {"Photo": photos[0], "Folders": []models.Folder{folder}},
	}
	for name, data := range pages {
		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, name, data); err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		out := buf.String()
		if name == "public/photo.html" && (!strings.Contains(out, "Lens corrected") || !strings.Contains(out, "unedited=1")) {
			t.Errorf("photo page is missing the develop summary")
		}
		if name != "admin/photo_edit.html" && !strings.Contains(out, "?v="+hash) {
			t.Errorf("%s: no versioned URL for the edited photo", name)
		}
	}
}

// TestDevelopPageImportMap renders the editor page and checks that every ES
// module is mapped to its content-hashed URL, so nginx's long static cache
// can never serve a stale module.
func TestDevelopPageImportMap(t *testing.T) {
	tmpl, err := handlers.LoadTemplates(webFS)
	if err != nil {
		t.Fatalf("LoadTemplates: %v", err)
	}
	var buf bytes.Buffer
	err = tmpl.ExecuteTemplate(&buf, "admin/develop.html", map[string]interface{}{
		"Title":  "Develop x",
		"Config": map[string]interface{}{"id": 1, "filename": "</script>x.jpg", "strip": []int{}, "back": "/admin/photos/1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, m := range []string{"main.js", "api.js", "panels.js", "shortcuts.js"} {
		if !strings.Contains(out, `/static/js/develop/`+m+`?v=`) {
			t.Errorf("import map / module URL missing versioned %s", m)
		}
	}
	if strings.Contains(out, "</script>x.jpg") {
		t.Error("config not escaped inside <script>")
	}
}
