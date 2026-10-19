package routes

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// Both views must describe the selected default branch, even when HEAD differs.
func TestLanguageViewsUseDefaultBranch(t *testing.T) {
	c, _ := testWorkspace(t)
	path := filepath.Join(c.Repo.ScanPath, "sample")
	repo, err := gogit.PlainOpen(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("develop"))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "script.py"), []byte(strings.Repeat("print('hello')\n", 100)), 0644); err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("script.py"); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Commit("Add Python on develop", &gogit.CommitOptions{Author: &object.Signature{Name: "Test", Email: "test@example.com", When: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	for _, branch := range []string{"main", "develop"} {
		c.Repo.MainBranch = []string{branch}
		handler := Handlers(c)
		for _, url := range []string{"/", "/sample"} {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest("GET", url, nil))
			body := w.Body.String()
			if w.Code != 200 {
				t.Fatalf("%s on %s: status %d", url, branch, w.Code)
			}
			if strings.Contains(body, "ZgotmplZ") {
				t.Fatal("template rejected language colors or bar sizes")
			}
			if url == "/" {
				language := "Go"
				if branch == "develop" {
					language = "Python"
				}
				if !strings.Contains(body, `class="repo-language"`) || !strings.Contains(body, `</span>`+language+`</span>`) {
					t.Errorf("index on %s: missing primary language %s", branch, language)
				}
			} else {
				if !strings.Contains(body, `class="language-bar"`) || !strings.Contains(body, `class="language-legend"`) {
					t.Fatal("repository page missing language breakdown")
				}
				if branch == "main" && !strings.Contains(body, "100.0%") {
					t.Fatal("single language share should be 100%")
				}
				if branch == "develop" && !strings.Contains(body, "<strong>Python</strong>") {
					t.Fatal("branch language statistics were not refreshed")
				}
			}
		}
	}
}
