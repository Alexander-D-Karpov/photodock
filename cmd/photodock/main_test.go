package main

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Alexander-D-Karpov/photodock/internal/config"
	"github.com/Alexander-D-Karpov/photodock/internal/handlers"
)

// newTestServer builds a route mux backed by the real embedded FS. db/thumb/scan
// are nil, so only DB-free routes (favicon, static) may be exercised.
func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	h := handlers.New(nil, &config.Config{}, nil, nil, webFS)
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
	for _, name := range []string{"public/index.html", "public/folder.html", "public/photo.html"} {
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
	for _, name := range []string{"js/gallery.js", "js/index.js", "css/public.css", "icon.webp"} {
		if _, err := fs.Stat(staticFS, name); err != nil {
			t.Errorf("missing embedded static asset %q: %v", name, err)
		}
	}
}
