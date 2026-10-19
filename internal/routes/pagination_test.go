package routes

import (
	"bytes"
	"fmt"
	"html"
	"log"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	gogit "github.com/go-git/go-git/v5"
)

// Verify filtering, ordering, and navigation against more than two pages of real repositories.
func TestRepositoryPagination(t *testing.T) {
	c, _ := testWorkspace(t)
	source := filepath.Join(c.Repo.ScanPath, "sample")
	for i := 1; i <= 24; i++ {
		name := fmt.Sprintf("repo-%02d.git", i)
		target := filepath.Join(c.Repo.ScanPath, name)
		if _, err := gogit.PlainClone(target, false, &gogit.CloneOptions{URL: source}); err != nil {
			t.Fatal(err)
		}
		description := "Other project"
		if i <= 21 {
			description = "Web & tools"
		}
		if err := os.WriteFile(filepath.Join(target, "description"), []byte(description), 0644); err != nil {
			t.Fatal(err)
		}
	}
	c.Repo.Ignore = []string{"sample", "repo-24.git"}
	c.Repo.Unlisted = []string{"repo-23.git"}
	c.Repo.Pinned = []string{"repo-20.git", "repo-03"}
	handler := Handlers(c)
	cardPattern := regexp.MustCompile(`data-repo="([^"]+)"`)
	sidebarPattern := regexp.MustCompile(`class="side-repo"`)
	names := func(indices ...int) []string {
		result := make([]string, len(indices))
		for i, n := range indices {
			result[i] = fmt.Sprintf("repo-%02d", n)
		}
		return result
	}
	first := names(20, 3, 1, 2, 4, 5, 6, 7, 8, 9)
	last := names(21, 22)
	for _, tt := range []struct {
		path  string
		want  []string
		label string
	}{
		{"/", first, "Showing 1–10 of"},
		{"/?page=2", names(10, 11, 12, 13, 14, 15, 16, 17, 18, 19), "Showing 11–20 of"},
		{"/?page=3", last, "Showing 21–22 of"},
		{"/?page=999", last, "Page 3 of 3"},
		{"/?page=-1", first, "Page 1 of 3"},
		{"/?page=0", first, "Page 1 of 3"},
		{"/?page=invalid", first, "Page 1 of 3"},
		{"/?page=999999999999999999999999999999", first, "Page 1 of 3"},
		{"/?sort=name", names(1, 2, 3, 4, 5, 6, 7, 8, 9, 10), "Sorted by"},
		{"/?sort=name&page=2", names(11, 12, 13, 14, 15, 16, 17, 18, 19, 20), "Page 2 of 3"},
		{"/?q=REPO-22", names(22), "Showing 1–1 of"},
		{"/?q=absent", nil, "No repositories found"},
		{"/?q=repo-23", nil, "No repositories found"},
		{"/?q=repo-24", nil, "No repositories found"},
	} {
		t.Run(tt.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest("GET", tt.path, nil))
			if w.Code != 200 {
				t.Fatalf("status %d", w.Code)
			}
			body := w.Body.String()
			var got []string
			for _, match := range cardPattern.FindAllStringSubmatch(body, -1) {
				got = append(got, match[1])
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("repositories %v; want %v", got, tt.want)
			}
			if len(sidebarPattern.FindAllString(body, -1)) != len(tt.want) {
				t.Error("sidebar loads repositories outside the current page")
			}
			if !strings.Contains(body, tt.label) {
				t.Errorf("missing page label %q", tt.label)
			}
		})
	}
	// Navigation retains both the search query (including special characters) and sorting.
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/?q=Web+%26+tools&sort=name&page=2", nil))
	links := regexp.MustCompile(`rel="(prev|next)" href="([^"]+)"`).FindAllStringSubmatch(w.Body.String(), -1)
	if len(links) != 2 {
		t.Fatalf("navigation links: %v", links)
	}
	for _, link := range links {
		target, err := url.Parse(html.UnescapeString(link[2]))
		if err != nil {
			t.Fatal(err)
		}
		if target.Query().Get("q") != "Web & tools" || target.Query().Get("sort") != "name" {
			t.Errorf("navigation lost filters: %s", target)
		}
		wantPage := "1"
		if link[1] == "next" {
			wantPage = "3"
		}
		if target.Query().Get("page") != wantPage {
			t.Errorf("%s points to %s", link[1], target)
		}
	}
	c.Repo.ScanPath = t.TempDir()
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/?page=999", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "No repositories yet") || strings.Contains(w.Body.String(), `class="pagination"`) {
		t.Fatal("empty repository directory should have no pagination")
	}
}

// A broken source tree on another page must not be opened for language analysis.
func TestPaginationDefersLanguageScans(t *testing.T) {
	c, _ := testWorkspace(t)
	source := filepath.Join(c.Repo.ScanPath, "sample")
	c.Repo.Ignore = []string{"sample"}
	for i := 0; i < 10; i++ {
		if _, err := gogit.PlainClone(filepath.Join(c.Repo.ScanPath, fmt.Sprintf("repo-%02d", i)), false, &gogit.CloneOptions{URL: source}); err != nil {
			t.Fatal(err)
		}
	}
	brokenConfig, _ := testWorkspace(t)
	brokenPath := filepath.Join(c.Repo.ScanPath, "zz-broken")
	if err := os.Rename(filepath.Join(brokenConfig.Repo.ScanPath, "sample"), brokenPath); err != nil {
		t.Fatal(err)
	}
	repo, err := gogit.PlainOpen(brokenPath)
	if err != nil {
		t.Fatal(err)
	}
	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		t.Fatal(err)
	}
	hash := commit.TreeHash.String()
	if err := os.Remove(filepath.Join(brokenPath, ".git", "objects", hash[:2], hash[2:])); err != nil {
		t.Fatal(err)
	}
	var messages bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&messages)
	defer log.SetOutput(previous)
	handler := Handlers(c)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 200 || strings.Contains(messages.String(), "languages for zz-broken") {
		t.Fatal("first page scanned an off-page source tree")
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/?page=2", nil))
	if w.Code != 200 || !strings.Contains(messages.String(), "languages for zz-broken") {
		t.Fatal("source tree should be inspected when its page is requested")
	}
}
