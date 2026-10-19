package routes

import (
	"context"
	"log"
	"os"
	"slices"
	"sync"
	"time"

	securejoin "github.com/cyphar/filepath-securejoin"
	"gitview/internal/git"
)

// repositoryInfo is immutable catalog metadata; display fields are filled in a request's copy.
type repositoryInfo struct {
	DisplayName, Name, Desc, Idle string
	Languages                     []git.LanguageStat
	path, ref                     string
	updatedAt                     time.Time
	languages                     *repositoryLanguages
}

type repositoryCatalog struct {
	mu         sync.Mutex
	entries    []repositoryInfo
	ready      bool
	validUntil time.Time
	pending    chan struct{}
	lastError  error
	interval   time.Duration
	load       func([]repositoryInfo) ([]repositoryInfo, error)
}

// newRepositoryCatalog refreshes on demand, with no idle worker or lifecycle to manage.
func newRepositoryCatalog(interval time.Duration, load func([]repositoryInfo) ([]repositoryInfo, error)) *repositoryCatalog {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &repositoryCatalog{interval: interval, load: load}
}

// snapshot shares cold initialization and serves stale data during a single background refresh.
func (c *repositoryCatalog) snapshot(ctx context.Context) ([]repositoryInfo, error) {
	for {
		c.mu.Lock()
		if c.ready {
			entries := slices.Clone(c.entries)
			if c.pending == nil && !time.Now().Before(c.validUntil) {
				c.startRefresh()
			}
			c.mu.Unlock()
			return entries, nil
		}
		if c.pending == nil {
			if c.lastError != nil && time.Now().Before(c.validUntil) {
				err := c.lastError
				c.mu.Unlock()
				return nil, err
			}
			c.startRefresh()
		}
		pending := c.pending
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-pending:
		}
	}
}

// startRefresh requires mu; the loader runs without holding up readers.
func (c *repositoryCatalog) startRefresh() {
	c.pending = make(chan struct{})
	previous := c.entries
	go func() {
		entries, err := c.load(previous)
		c.mu.Lock()
		defer c.mu.Unlock()
		if err == nil {
			c.entries = entries
			c.ready = true
		} else {
			log.Printf("refreshing repository catalog: %v", err)
		}
		c.lastError = err
		c.validUntil = time.Now().Add(c.interval)
		close(c.pending)
		c.pending = nil
	}()
}

// scanCatalog reads only current-commit metadata, preserving reusable language results.
func (d *deps) scanCatalog(previous []repositoryInfo) ([]repositoryInfo, error) {
	dirs, err := os.ReadDir(d.c.Repo.ScanPath)
	if err != nil {
		return nil, err
	}
	old := make(map[string]repositoryInfo, len(previous))
	for _, repo := range previous {
		old[repo.path] = repo
	}
	entries := make([]repositoryInfo, 0, len(dirs))
	for _, dir := range dirs {
		name := dir.Name()
		if !dir.IsDir() || d.isIgnored(name) || d.isUnlisted(name) {
			continue
		}
		path, err := securejoin.SecureJoin(d.c.Repo.ScanPath, name)
		if err != nil {
			return nil, err
		}
		gr, err := git.OpenDefault(path, d.c.Repo.MainBranch)
		if err != nil {
			if cached, ok := old[path]; ok {
				entries = append(entries, cached)
			}
			log.Printf("reading repository %s: %v", name, err)
			continue
		}
		commit, err := gr.LastCommit()
		if err != nil {
			if cached, ok := old[path]; ok {
				entries = append(entries, cached)
			}
			log.Printf("reading repository %s: %v", name, err)
			continue
		}
		ref := commit.Commit().Hash.String()
		languages := &repositoryLanguages{}
		if cached, ok := old[path]; ok && cached.ref == ref {
			languages = cached.languages
		}
		entries = append(entries, repositoryInfo{
			DisplayName: getDisplayName(name), Name: name, Desc: getDescription(path),
			path: path, ref: ref, updatedAt: commit.Commit().Author.When, languages: languages,
		})
	}
	return entries, nil
}

type repositoryLanguages struct {
	mu    sync.Mutex
	ready bool
	stats []git.LanguageStat
}

// languageStats avoids Git I/O for repeated page views and retries transient read errors.
func (r *repositoryInfo) languageStats() ([]git.LanguageStat, error) {
	r.languages.mu.Lock()
	defer r.languages.mu.Unlock()
	if r.languages.ready {
		return r.languages.stats, nil
	}
	gr, err := git.Open(r.path, r.ref)
	if err != nil {
		return nil, err
	}
	stats, err := gr.Languages()
	if err != nil {
		return nil, err
	}
	r.languages.stats = stats
	r.languages.ready = true
	return stats, nil
}
