package git

import (
	"fmt"
	"io"
	"slices"
	"sort"
	"sync"

	enry "github.com/go-enry/go-enry/v2"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// LanguageStat describes a language's share of the repository's source bytes.
type LanguageStat struct {
	Name       string
	Color      string
	Bytes      int64
	Percentage float64
}

// Immutable Git tree IDs keep cached statistics valid across mirror updates.
var languageStatsCache = struct {
	sync.Mutex
	entries map[plumbing.Hash][]LanguageStat
	order   []plumbing.Hash
}{entries: make(map[plumbing.Hash][]LanguageStat)}

// Languages classifies committed source files without requiring a working tree.
func (g *GitRepo) Languages() ([]LanguageStat, error) {
	commit, err := g.r.CommitObject(g.h)
	if err != nil {
		return nil, fmt.Errorf("language commit: %w", err)
	}
	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("language tree: %w", err)
	}
	languageStatsCache.Lock()
	cached, ok := languageStatsCache.entries[tree.Hash]
	languageStatsCache.Unlock()
	if ok {
		return slices.Clone(cached), nil
	}

	sizes := make(map[string]int64)
	err = tree.Files().ForEach(func(file *object.File) error {
		if file.Size == 0 || file.Mode == filemode.Symlink || enry.IsVendor(file.Name) || enry.IsDocumentation(file.Name) {
			return nil
		}
		reader, err := file.Reader()
		if err != nil {
			return err
		}
		// Bound content inspection while weighing every included file by its full size.
		content, readErr := io.ReadAll(io.LimitReader(reader, 64*1024))
		closeErr := reader.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if enry.IsBinary(content) || enry.IsGenerated(file.Name, content) {
			return nil
		}
		name := enry.GetLanguage(file.Name, content)
		kind := enry.GetLanguageType(name)
		if kind != enry.Programming && kind != enry.Markup {
			return nil
		}
		if group := enry.GetLanguageGroup(name); group != "" {
			name = group
		}
		sizes[name] += file.Size
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("classifying languages: %w", err)
	}
	stats := make([]LanguageStat, 0, len(sizes))
	var total int64
	for name, size := range sizes {
		stats = append(stats, LanguageStat{Name: name, Color: enry.GetColor(name), Bytes: size})
		total += size
	}
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].Bytes == stats[j].Bytes {
			return stats[i].Name < stats[j].Name
		}
		return stats[i].Bytes > stats[j].Bytes
	})
	for i := range stats {
		stats[i].Percentage = 100 * float64(stats[i].Bytes) / float64(total)
	}
	languageStatsCache.Lock()
	defer languageStatsCache.Unlock()
	if _, exists := languageStatsCache.entries[tree.Hash]; !exists {
		const maxCachedTrees = 128
		if len(languageStatsCache.order) == maxCachedTrees {
			delete(languageStatsCache.entries, languageStatsCache.order[0])
			languageStatsCache.order = languageStatsCache.order[1:]
		}
		languageStatsCache.order = append(languageStatsCache.order, tree.Hash)
		languageStatsCache.entries[tree.Hash] = slices.Clone(stats)
	}
	return stats, nil
}
