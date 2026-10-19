package routes

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"gitview/internal/config"
)

// Exercise configured ordering through YAML loading and rendered repository cards.
func TestIndexPinnedOrder(t *testing.T) {
	c, _ := testWorkspace(t)
	source := filepath.Join(c.Repo.ScanPath, "sample")
	for name, day := range map[string]int{"first": 1, "second.git": 5, "recent": 10, "hidden": 20, "ignored": 25} {
		path := filepath.Join(c.Repo.ScanPath, name)
		repo, err := gogit.PlainClone(path, false, &gogit.CloneOptions{URL: source})
		if err != nil {
			t.Fatal(err)
		}
		wt, err := repo.Worktree()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "README.md"), []byte(name), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Add("README.md"); err != nil {
			t.Fatal(err)
		}
		author := &object.Signature{Name: "Test Author", Email: "author@example.com", When: time.Date(2026, 1, day, 12, 0, 0, 0, time.UTC)}
		if _, err := wt.Commit("Update "+name, &gogit.CommitOptions{Author: author}); err != nil {
			t.Fatal(err)
		}
	}
	cards := regexp.MustCompile(`data-repo="([^"]+)"`)
	for _, tt := range []struct {
		name         string
		pinned, want []string
	}{
		{"default", nil, []string{"recent", "second", "sample", "first"}},
		{"configured order", []string{"first", "second"}, []string{"first", "second", "recent", "sample"}},
		{"suffixes duplicates missing and hidden", []string{"hidden", "ignored", "missing", "second.git", "first", "second"}, []string{"second", "first", "recent", "sample"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pinned, err := json.Marshal(tt.pinned)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "config.yaml")
			yaml := fmt.Sprintf("repo:\n  scanPath: %q\n  pinned: %s\n  unlisted: [hidden]\n  ignore: [ignored]\ndirs:\n  templates: %q\n  static: %q\n", c.Repo.ScanPath, pinned, c.Dirs.Templates, c.Dirs.Static)
			if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
				t.Fatal(err)
			}
			loaded, err := config.Read(path)
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			Handlers(loaded).ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
			if w.Code != http.StatusOK {
				t.Fatalf("index status: %d", w.Code)
			}
			var got []string
			for _, match := range cards.FindAllStringSubmatch(w.Body.String(), -1) {
				got = append(got, match[1])
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("repository order: %v; want %v", got, tt.want)
			}
		})
	}
}

func TestRepositoryOwnerFromConfig(t *testing.T) {
	c, _ := testWorkspace(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	yaml := fmt.Sprintf("repo:\n  scanPath: %q\n  mainBranch: [main]\ndirs:\n  templates: %q\n  static: %q\nmeta:\n  title: git.example.com\n  username: arnaucube\n", c.Repo.ScanPath, c.Dirs.Templates, c.Dirs.Static)
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/sample", "/sample/tree/main/", "/sample/blob/main/src/main.go", "/sample/log/main", "/sample/refs/"} {
		w := httptest.NewRecorder()
		Handlers(loaded).ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `<a href="/">arnaucube</a>`) {
			t.Errorf("%s: missing configured owner (status %d)", path, w.Code)
		}
	}
}
