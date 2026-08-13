package handlers

import (
	"bytes"
	"html/template"
	"testing"
	"testing/fstest"
)

func TestFormatSize(t *testing.T) {
	cases := map[int64]string{
		0:          "0 B",
		512:        "512 B",
		1023:       "1023 B",
		1024:       "1.0 KB",
		1536:       "1.5 KB",
		1048576:    "1.0 MB",
		1073741824: "1.0 GB",
	}
	for in, want := range cases {
		if got := formatSize(in); got != want {
			t.Errorf("formatSize(%d) = %q; want %q", in, got, want)
		}
	}
}

func TestSanitizeFilename(t *testing.T) {
	cases := map[string]string{
		// Windows/Chrome collision-dedup suffix from MTP/picker copies must be stripped.
		"DSC00170[1].jpg":  "DSC00170.jpg",
		"IMG_1234[12].JPG": "IMG_1234.JPG",
		"photo (2).png":    "photo.png",
		"clip (10).arw":    "clip.arw",
		// No suffix -> unchanged.
		"DSC00170.jpg": "DSC00170.jpg",
		"IMG_1234.JPG": "IMG_1234.JPG",
		// Bracketed non-numeric text is NOT a dedup suffix; keep it.
		"final[edit].jpg": "final[edit].jpg",
		// Path components and traversal are stripped as before.
		"../../etc/passwd.jpg": "passwd.jpg",
		`sub\evil.jpg`:         "sub_evil.jpg",
		// Illegal chars mapped to underscore (existing behavior preserved).
		"a:b*c?.png": "a_b_c_.png",
	}
	for in, want := range cases {
		if got := sanitizeFilename(in); got != want {
			t.Errorf("sanitizeFilename(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestEscapeURLPath(t *testing.T) {
	cases := map[string]string{
		"photo.jpg":   "photo.jpg",
		"a b/c":       "a%20b/c",
		"folder/sub":  "folder/sub",
		"Япония 2026": "%D0%AF%D0%BF%D0%BE%D0%BD%D0%B8%D1%8F%202026",
	}
	for in, want := range cases {
		if got := escapeURLPath(in); got != want {
			t.Errorf("escapeURLPath(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestStaticAssetVersions(t *testing.T) {
	fsA := fstest.MapFS{
		"web/static/js/gallery.js":  {Data: []byte("console.log(1)")},
		"web/static/css/public.css": {Data: []byte("body{}")},
		"web/templates/x.html":      {Data: []byte("<html></html>")}, // must be ignored
	}
	v := staticAssetVersions(fsA)

	got, ok := v["/static/js/gallery.js"]
	if !ok {
		t.Fatalf("expected version for /static/js/gallery.js, got none")
	}
	if len(got) != 8 {
		t.Errorf("version hash length = %d (%q); want 8", len(got), got)
	}
	if _, ok := v["/static/templates/x.html"]; ok {
		t.Errorf("template file must not be versioned as a static asset")
	}
	if _, ok := v["/templates/x.html"]; ok {
		t.Errorf("non-static file leaked into asset versions")
	}

	// A byte change must change the hash (cache-bust).
	fsB := fstest.MapFS{
		"web/static/js/gallery.js": {Data: []byte("console.log(2)")},
	}
	if got2 := staticAssetVersions(fsB)["/static/js/gallery.js"]; got2 == got {
		t.Errorf("changing file bytes did not change version hash (%q)", got2)
	}
}

func TestTemplateFuncMapAsset(t *testing.T) {
	fm := templateFuncMap(map[string]string{"/static/js/gallery.js": "abc12345"})
	asset, ok := fm["asset"].(func(string) string)
	if !ok {
		t.Fatal("asset func missing from func map")
	}
	if got := asset("/static/js/gallery.js"); got != "/static/js/gallery.js?v=abc12345" {
		t.Errorf("asset(known) = %q; want versioned path", got)
	}
	if got := asset("/static/unknown.js"); got != "/static/unknown.js" {
		t.Errorf("asset(unknown) = %q; want path unchanged", got)
	}

	// nil version map: asset must not panic and returns path unchanged.
	nilFM := templateFuncMap(nil)
	nilAsset := nilFM["asset"].(func(string) string)
	if got := nilAsset("/static/js/gallery.js"); got != "/static/js/gallery.js" {
		t.Errorf("asset(nil map) = %q; want path unchanged", got)
	}
}

// TestTemplateFuncMapRenders exercises the func map through an actual template
// render, matching how templates invoke asset in an attribute context.
func TestTemplateFuncMapRenders(t *testing.T) {
	fm := templateFuncMap(map[string]string{"/static/css/public.css": "deadbeef"})
	tmpl := template.Must(template.New("t").Funcs(fm).Parse(
		`<link href="{{asset "/static/css/public.css"}}">`))
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, nil); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := buf.String(); got != `<link href="/static/css/public.css?v=deadbeef">` {
		t.Errorf("rendered = %q", got)
	}
}
