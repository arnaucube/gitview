package routes

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"gitview/internal/config"
)

// testWorkspace uses real Git objects so the handlers exercise repository behavior.
func testWorkspace(t *testing.T) (*config.Config, string) {
	t.Helper()
	scan := t.TempDir()
	path := filepath.Join(scan, "sample")
	repo, err := gogit.PlainInit(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main"))); err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"README.md": "# Sample project\n\nA useful README.\n<script>alert('unsafe')</script>", "src/main.go": "package main\n\nfunc main() {}\n", "plain.unknownextension": "hello <world>\n"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(path, name)), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := wt.Add("."); err != nil {
		t.Fatal(err)
	}
	signature := &object.Signature{Name: "Test Author", Email: "author@example.com", When: time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)}
	hash, err := wt.Commit("Initial project", &gogit.CommitOptions{Author: signature})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateTag("v1.0.0", hash, nil); err != nil {
		t.Fatal(err)
	}
	if err := repo.Storer.SetReference(plumbing.NewHashReference(plumbing.NewBranchReferenceName("develop"), hash)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "description"), []byte("A sample repository"), 0644); err != nil {
		t.Fatal(err)
	}
	c := &config.Config{}
	c.Repo.ScanPath = scan
	c.Repo.MainBranch = []string{"main", "master"}
	c.Repo.Readme = []string{"README.md"}
	c.Dirs.Templates, _ = filepath.Abs("../../templates")
	c.Dirs.Static, _ = filepath.Abs("../../static")
	c.Meta.Title = "Gitview"
	c.Meta.SyntaxHighlight = "github"
	return c, hash.String()
}

func TestRepositoryBrowsing(t *testing.T) {
	c, hash := testWorkspace(t)
	handler := Handlers(c)
	for _, tt := range []struct {
		path, contains string
		status         int
	}{
		{"/", "sample", 200}, {"/sample", "Sample project", 200},
		{"/sample/tree/main/", "README.md", 200}, {"/sample/tree/main/src", "main.go", 200},
		{"/sample/blob/main/src/main.go", "chroma", 200}, {"/sample/blob/main/plain.unknownextension", "hello", 200},
		{"/sample/blob/main/src/main.go?raw=true", "package main", 200},
		{"/sample/log/main", "Initial project", 200}, {"/sample/commit/" + hash, "diff-add", 200},
		{"/sample/refs/", "v1.0.0", 200}, {"/static/app.js", "querySelector", 200},
		{"/missing", "could not be found", 404}, {"/sample/tree/missing/", "could not be found", 404},
		{"/sample/blob/main/missing", "could not be found", 404}, {"/sample/tree/main/missing", "could not be found", 404},
		{"/sample/unknown", "could not be found", 404}, {"/unknown/path/again", "could not be found", 404},
	} {
		t.Run(tt.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest("GET", tt.path, nil))
			if w.Code != tt.status || !strings.Contains(w.Body.String(), tt.contains) {
				t.Fatalf("status %d, expected %d containing %q; body: %.500s", w.Code, tt.status, tt.contains, w.Body.String())
			}
		})
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/sample", nil))
	if strings.Contains(w.Body.String(), "<script>alert") {
		t.Fatal("README contains executable script")
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/sample/blob/main/src/main.go?raw=true", nil))
	if got := w.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("raw content type: %s", got)
	}
}

func TestArchiveContents(t *testing.T) {
	c, _ := testWorkspace(t)
	w := httptest.NewRecorder()
	Handlers(c).ServeHTTP(w, httptest.NewRequest("GET", "/sample/archive/main.tar.gz", nil))
	if w.Code != 200 {
		t.Fatalf("archive status: %d", w.Code)
	}
	gz, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	found := false
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Name == "sample-main/src/main.go" {
			b, err := io.ReadAll(tr)
			if err != nil {
				t.Fatal(err)
			}
			found = string(b) == "package main\n\nfunc main() {}\n"
		}
	}
	if !found {
		t.Fatal("archive did not contain expected source")
	}
}

func TestVisibilityAndPushPolicy(t *testing.T) {
	c, _ := testWorkspace(t)
	c.Repo.Unlisted = []string{"sample"}
	handler := Handlers(c)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if strings.Contains(w.Body.String(), "data-repo=\"sample\"") {
		t.Fatal("unlisted repository is visible")
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/sample", nil))
	if w.Code != 200 {
		t.Fatal("unlisted repository is not accessible")
	}
	c.Repo.Ignore = []string{"sample"}
	for _, path := range []string{"/sample", "/sample/info/refs?service=git-upload-pack", "/sample/archive/main.tar.gz"} {
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Fatalf("ignored repo accessible: %s (%d)", path, w.Code)
		}
	}
	for _, method := range []string{"GET", "POST"} {
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(method, "/sample/git-receive-pack", nil))
		if w.Code != 400 {
			t.Fatalf("push not rejected: %d", w.Code)
		}
	}
}

func TestSmartHTTPClone(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git executable unavailable")
	}
	c, hash := testWorkspace(t)
	server := httptest.NewServer(Handlers(c))
	defer server.Close()
	target := filepath.Join(t.TempDir(), "clone")
	cmd := exec.Command("git", "clone", server.URL+"/sample", target)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone: %v: %s", err, out)
	}
	repo, err := gogit.PlainOpen(target)
	if err != nil {
		t.Fatal(err)
	}
	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	if head.Hash().String() != hash {
		t.Fatal("clone HEAD differs from source")
	}
	resp, err := http.Get(server.URL + "/sample/info/refs?extra=1&service=git-upload-pack")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Type") != "application/x-git-upload-pack-advertisement" {
		t.Fatal("smart HTTP query routing failed")
	}
}

func TestCustomDefaultBranchAndEmptyRepository(t *testing.T) {
	c, hash := testWorkspace(t)
	repo, err := gogit.PlainOpen(filepath.Join(c.Repo.ScanPath, "sample"))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Storer.SetReference(plumbing.NewHashReference(plumbing.NewBranchReferenceName("trunk"), plumbing.NewHash(hash))); err != nil {
		t.Fatal(err)
	}
	if err := repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("trunk"))); err != nil {
		t.Fatal(err)
	}
	c.Repo.MainBranch = []string{"nonexistent"}
	if _, err := gogit.PlainInit(filepath.Join(c.Repo.ScanPath, "empty"), false); err != nil {
		t.Fatal(err)
	}
	handler := Handlers(c)
	for _, path := range []string{"/", "/sample"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 {
			t.Fatalf("%s: status %d", path, w.Code)
		}
		if path == "/sample" && !strings.Contains(w.Body.String(), "/sample/tree/trunk/") {
			t.Fatal("default branch did not fall back to HEAD")
		}
	}
}
