package routes

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gitview/internal/config"
)

// Verify configured branding renders consistently and resolves to the served asset.
func TestConfiguredBranding(t *testing.T) {
	c, _ := testWorkspace(t)
	static := t.TempDir()
	if err := os.MkdirAll(filepath.Join(static, "branding"), 0755); err != nil {
		t.Fatal(err)
	}
	const svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"><circle r="1"/></svg>`
	for _, name := range []string{"favicon.svg", "branding/owner mark.svg"} {
		if err := os.WriteFile(filepath.Join(static, name), []byte(svg), 0644); err != nil {
			t.Fatal(err)
		}
	}
	favicon := regexp.MustCompile(`<link rel="icon" href="([^"]+)"`)
	logo := regexp.MustCompile(`<img class="brand-mark" src="([^"]+)"`)
	for _, tt := range []struct{ name, settings, asset string }{
		{"logo overrides legacy favicon", "  logo: branding/owner mark.svg\n  favicon: nonexistent.svg\n", "/static/branding/owner%20mark.svg"},
		{"legacy favicon", "  favicon: branding/owner mark.svg\n", "/static/branding/owner%20mark.svg"},
		{"default logo", "", "/static/favicon.svg"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			yaml := fmt.Sprintf("repo:\n  scanPath: %q\n  mainBranch: [main]\ndirs:\n  templates: %q\n  static: %q\nmeta:\n  username: arnaucube\n  title: browser-only-title\n%s", c.Repo.ScanPath, c.Dirs.Templates, static, tt.settings)
			if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
				t.Fatal(err)
			}
			loaded, err := config.Read(path)
			if err != nil {
				t.Fatal(err)
			}
			handler := Handlers(loaded)
			for _, page := range []struct {
				url    string
				status int
			}{{"/", 200}, {"/sample", 200}, {"/missing", 404}} {
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, httptest.NewRequest("GET", page.url, nil))
				if w.Code != page.status {
					t.Fatalf("%s: status %d", page.url, w.Code)
				}
				body := w.Body.String()
				icon := favicon.FindStringSubmatch(body)
				brand := logo.FindStringSubmatch(body)
				if len(icon) != 2 || len(brand) != 2 {
					t.Fatalf("%s: missing favicon or logo", page.url)
				}
				if icon[1] != tt.asset || brand[1] != tt.asset {
					t.Errorf("%s: favicon %q, logo %q; want %q", page.url, icon[1], brand[1], tt.asset)
				}
				_, header, found := strings.Cut(body, `<header class="topbar">`)
				if !found {
					t.Fatalf("%s: missing topbar", page.url)
				}
				topbar, _, _ := strings.Cut(header, "</header>")
				if !strings.Contains(topbar, `class="brand-name">arnaucube</span>`) || strings.Contains(topbar, "browser-only-title") || strings.Contains(topbar, "top-context") {
					t.Errorf("%s: topbar does not use only the configured username", page.url)
				}
				if !strings.Contains(body, "browser-only-title</title>") {
					t.Errorf("%s: browser title was lost", page.url)
				}
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest("GET", tt.asset, nil))
			if w.Code != http.StatusOK || w.Body.String() != svg {
				t.Fatalf("logo request: status %d, body %q", w.Code, w.Body.String())
			}
			if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "image/svg+xml") {
				t.Errorf("logo content type: %q", got)
			}
			loaded.Repo.ScanPath = filepath.Join(t.TempDir(), "missing")
			handler = Handlers(loaded)
			w = httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
			if w.Code != 500 || !strings.Contains(w.Body.String(), tt.asset) {
				t.Errorf("server error page lost configured branding: status %d", w.Code)
			}
		})
	}
}
