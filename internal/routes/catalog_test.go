package routes

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// expireCatalog advances the refresh boundary without sleeps or a background ticker.
func expireCatalog(c *repositoryCatalog) {
	c.mu.Lock()
	c.validUntil = time.Time{}
	c.mu.Unlock()
}

// waitCatalog waits for publication, not merely for the loader to finish reading.
func waitCatalog(t *testing.T, c *repositoryCatalog) {
	t.Helper()
	c.mu.Lock()
	pending := c.pending
	c.mu.Unlock()
	if pending == nil {
		return
	}
	select {
	case <-pending:
	case <-time.After(5 * time.Second):
		t.Fatal("catalog refresh did not finish")
	}
}

func TestCatalogConcurrentInitialization(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	cache := newRepositoryCatalog(time.Hour, func(_ []repositoryInfo) ([]repositoryInfo, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return []repositoryInfo{{Name: "sample"}}, nil
	})
	type result struct {
		entries []repositoryInfo
		err     error
	}
	results := make(chan result, 20)
	for i := 0; i < 20; i++ {
		go func() { entries, err := cache.snapshot(context.Background()); results <- result{entries, err} }()
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("catalog did not initialize")
	}
	// A disconnected request must not cancel initialization shared by other visitors.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cache.snapshot(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled request: %v", err)
	}
	releaseOnce.Do(func() { close(release) })
	for i := 0; i < 20; i++ {
		select {
		case result := <-results:
			if result.err != nil || len(result.entries) != 1 || result.entries[0].Name != "sample" {
				t.Fatalf("catalog result: %+v", result)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("request did not finish")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("concurrent visitors started %d scans", calls.Load())
	}
	entries, err := cache.snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	entries[0].Name = "request-local change"
	next, err := cache.snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if next[0].Name != "sample" {
		t.Fatal("request changed the shared catalog")
	}
}

func TestCatalogServesStaleDataAndSurvivesRefreshFailure(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	cache := newRepositoryCatalog(time.Hour, func(_ []repositoryInfo) ([]repositoryInfo, error) {
		switch calls.Add(1) {
		case 1:
			return []repositoryInfo{{Name: "old"}}, nil
		case 2:
			close(started)
			<-release
			return []repositoryInfo{{Name: "updated"}}, nil
		case 3:
			return nil, errors.New("temporary scan failure")
		default:
			return []repositoryInfo{{Name: "recovered"}}, nil
		}
	})
	if _, err := cache.snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	expireCatalog(cache)
	response := make(chan []repositoryInfo, 1)
	go func() { entries, _ := cache.snapshot(context.Background()); response <- entries }()
	select {
	case entries := <-response:
		if entries[0].Name != "old" {
			t.Fatal("stale snapshot was lost")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("page request waited for a refresh")
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("background refresh did not start")
	}
	for i := 0; i < 20; i++ {
		entries, err := cache.snapshot(context.Background())
		if err != nil || entries[0].Name != "old" {
			t.Fatal("refresh blocked access to the previous snapshot")
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("overlapping refreshes: %d", calls.Load())
	}
	releaseOnce.Do(func() { close(release) })
	waitCatalog(t, cache)
	entries, err := cache.snapshot(context.Background())
	if err != nil || entries[0].Name != "updated" {
		t.Fatal("updated snapshot was not published")
	}
	expireCatalog(cache)
	if _, err := cache.snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitCatalog(t, cache)
	entries, err = cache.snapshot(context.Background())
	if err != nil || entries[0].Name != "updated" {
		t.Fatal("temporary failure discarded the previous snapshot")
	}
	if calls.Load() != 3 {
		t.Fatal("failed refresh was retried before the next interval")
	}
	expireCatalog(cache)
	if _, err := cache.snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitCatalog(t, cache)
	entries, err = cache.snapshot(context.Background())
	if err != nil || entries[0].Name != "recovered" {
		t.Fatal("catalog did not recover after a failed refresh")
	}
}

func TestCatalogInitialFailureCanRecover(t *testing.T) {
	var calls int
	cache := newRepositoryCatalog(time.Hour, func(_ []repositoryInfo) ([]repositoryInfo, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("scan path unavailable")
		}
		return []repositoryInfo{}, nil
	})
	if _, err := cache.snapshot(context.Background()); err == nil {
		t.Fatal("initial scan failure was hidden")
	}
	if _, err := cache.snapshot(context.Background()); err == nil {
		t.Fatal("initial scan error was not retained")
	}
	if calls != 1 {
		t.Fatal("failed initial scan ran on every request")
	}
	expireCatalog(cache)
	entries, err := cache.snapshot(context.Background())
	if err != nil || len(entries) != 0 {
		t.Fatalf("recovered empty catalog: %v, %v", entries, err)
	}
}

func TestCatalogTracksMirrorChangesAndReusesLanguages(t *testing.T) {
	cfg, initialHash := testWorkspace(t)
	d := deps{c: cfg}
	cache := newRepositoryCatalog(time.Hour, d.scanCatalog)
	ctx := context.Background()
	entries, err := cache.snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatal("initial repository was not found")
	}
	if _, err := entries[0].languageStats(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfg.Repo.ScanPath, "sample")
	if err := os.WriteFile(filepath.Join(path, "description"), []byte("New description"), 0644); err != nil {
		t.Fatal(err)
	}
	expireCatalog(cache)
	if _, err := cache.snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	waitCatalog(t, cache)
	entries, err = cache.snapshot(ctx)
	if err != nil || entries[0].Desc != "New description" {
		t.Fatal("description did not refresh")
	}
	// An unchanged commit must use its cached language result with no repository I/O.
	func() {
		offline := filepath.Join(t.TempDir(), "offline")
		if err := os.Rename(path, offline); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := os.Rename(offline, path); err != nil {
				t.Error(err)
			}
		}()
		cached, err := cache.snapshot(ctx)
		if err != nil || len(cached) != 1 {
			t.Fatal("warm request rescanned the filesystem")
		}
		stats, err := cached[0].languageStats()
		if err != nil || len(stats) != 1 || stats[0].Name != "Go" {
			t.Fatal("unchanged repository repeated language analysis")
		}
	}()
	repo, err := gogit.PlainOpen(path)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "script.py"), []byte("print('Python takes more space than the Go source in this fixture')\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("script.py"); err != nil {
		t.Fatal(err)
	}
	updatedHash, err := wt.Commit("Mirror update", &gogit.CommitOptions{Author: &object.Signature{Name: "Test", Email: "test@example.com", When: time.Now()}})
	if err != nil {
		t.Fatal(err)
	}
	expireCatalog(cache)
	if _, err := cache.snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	waitCatalog(t, cache)
	entries, err = cache.snapshot(ctx)
	if err != nil || entries[0].ref != updatedHash.String() {
		t.Fatal("new commit did not refresh")
	}
	stats, err := entries[0].languageStats()
	if err != nil || len(stats) != 2 || stats[0].Name != "Python" {
		t.Fatal("new commit reused stale language results")
	}
	// A force-push can move the branch backwards; invalidation must not depend on dates.
	if err := repo.Storer.SetReference(plumbing.NewHashReference(plumbing.NewBranchReferenceName("main"), plumbing.NewHash(initialHash))); err != nil {
		t.Fatal(err)
	}
	expireCatalog(cache)
	if _, err := cache.snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	waitCatalog(t, cache)
	entries, err = cache.snapshot(ctx)
	if err != nil || entries[0].ref != initialHash {
		t.Fatal("force-pushed branch was not reflected")
	}
	stats, err = entries[0].languageStats()
	if err != nil || len(stats) != 1 || stats[0].Name != "Go" {
		t.Fatal("force-push did not refresh languages")
	}
	// Newly mirrored repositories appear and deleted directories disappear on refresh.
	added := filepath.Join(cfg.Repo.ScanPath, "added.git")
	if _, err := gogit.PlainClone(added, true, &gogit.CloneOptions{URL: path}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	expireCatalog(cache)
	if _, err := cache.snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	waitCatalog(t, cache)
	entries, err = cache.snapshot(ctx)
	if err != nil || len(entries) != 1 || entries[0].Name != "added.git" {
		t.Fatalf("changed repository directories: %+v, %v", entries, err)
	}
}
