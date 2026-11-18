package ui

import (
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func get(t *testing.T, h http.Handler, method, path string) (*http.Response, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	b, _ := io.ReadAll(rec.Result().Body)
	return rec.Result(), string(b)
}

func TestServesTheBuild(t *testing.T) {
	h := Handler(Prefix)
	cases := []struct {
		path, ctype, has string
	}{
		{Prefix, "text/html", `<script src="app.js">`},
		{Prefix + "index.html", "text/html", "<title>understudy</title>"},
		{Prefix + "app.js", "javascript", "/_understudy/"},
		{Prefix + "app.css", "text/css", "prefers-color-scheme"},
		{Prefix + "logo.svg", "image/svg+xml", "<svg"},
		{Prefix + "favicon-32.png", "image/png", "PNG"},
	}
	for _, c := range cases {
		r, body := get(t, h, "GET", c.path)
		if r.StatusCode != 200 && (c.path != Prefix+"index.html" || r.StatusCode != 301) {
			t.Errorf("%s: %d", c.path, r.StatusCode)
			continue
		}
		if r.StatusCode == 301 {
			continue
		}
		if !strings.Contains(r.Header.Get("Content-Type"), c.ctype) || !strings.Contains(body, c.has) {
			t.Errorf("%s: %s %.80q", c.path, r.Header.Get("Content-Type"), body)
		}
		if r.Header.Get("Cache-Control") != "no-cache" || !strings.Contains(r.Header.Get("Content-Security-Policy"), "default-src 'self'") {
			t.Errorf("%s: headers %v", c.path, r.Header)
		}
	}
	if r, _ := get(t, h, "GET", "/_understudy/ui"); r.StatusCode != 301 || r.Header.Get("Location") != Prefix {
		t.Errorf("no slash: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	if r, body := get(t, h, "GET", Prefix+"nope.js"); r.StatusCode != 404 || !strings.Contains(body, "the UI is at "+Prefix) {
		t.Errorf("missing: %d %s", r.StatusCode, body)
	}
	if r, _ := get(t, h, "POST", Prefix); r.StatusCode != 405 || r.Header.Get("Allow") != "GET, HEAD" {
		t.Errorf("post: %d", r.StatusCode)
	}
	if r, _ := get(t, h, "HEAD", Prefix+"app.js"); r.StatusCode != 200 {
		t.Errorf("head: %d", r.StatusCode)
	}
	if r, _ := get(t, h, "GET", Prefix+"../../etc/passwd"); r.StatusCode == 200 {
		t.Error("escaped the UI folder")
	}
}

// The UI loads nothing from outside: no CDN, no remote fonts.
func TestNoExternalResources(t *testing.T) {
	err := fs.WalkDir(Files(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := fs.ReadFile(Files(), p)
		for _, bad := range []string{"https://", "http://", "//cdn", "fonts.googleapis"} {
			if strings.Contains(string(b), bad) && !strings.HasSuffix(p, ".svg") && !strings.HasSuffix(p, ".png") {
				t.Errorf("%s refers to %s", p, bad)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Stat(Files(), "index.html"); err != nil {
		t.Fatal("the UI is not built: run npm ci && npm run build in ui/")
	}
}
