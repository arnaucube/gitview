package git

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

type GitRepo struct {
	r *git.Repository
	h plumbing.Hash
}

type TagList struct {
	refs []*TagReference
	r    *git.Repository
}

// TagReference is used to list both tag and non-annotated tags.
// Non-annotated tags should only contains a reference.
// Annotated tags should contain its reference and its tag information.
type TagReference struct {
	ref *plumbing.Reference
	tag *object.Tag
}

// CommitReference aggregate all the references for a given commit
type CommitReference struct {
	commit *object.Commit
	refs   []*plumbing.Reference
}

// infoWrapper wraps the property of a TreeEntry so it can export fs.FileInfo
// to tar WriteHeader
type infoWrapper struct {
	name    string
	size    int64
	mode    fs.FileMode
	modTime time.Time
	isDir   bool
}

func (t *TagList) Len() int {
	return len(t.refs)
}

func (t *TagList) Swap(i, j int) {
	t.refs[i], t.refs[j] = t.refs[j], t.refs[i]
}

// Less sorts tags in reverse chronological order.
func (t *TagList) Less(i, j int) bool {
	var dateI time.Time
	var dateJ time.Time

	if t.refs[i].tag != nil {
		dateI = t.refs[i].tag.Tagger.When
	} else {
		c, err := t.r.CommitObject(t.refs[i].ref.Hash())
		if err != nil {
			dateI = time.Now()
		} else {
			dateI = c.Committer.When
		}
	}

	if t.refs[j].tag != nil {
		dateJ = t.refs[j].tag.Tagger.When
	} else {
		c, err := t.r.CommitObject(t.refs[j].ref.Hash())
		if err != nil {
			dateJ = time.Now()
		} else {
			dateJ = c.Committer.When
		}
	}

	return dateI.After(dateJ)
}

func Open(path string, ref string) (*GitRepo, error) {
	var err error
	g := GitRepo{}
	g.r, err = git.PlainOpen(path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}

	if ref == "" {
		head, err := g.r.Head()
		if err != nil {
			return nil, fmt.Errorf("getting head of %s: %w", path, err)
		}
		g.h = head.Hash()
	} else {
		hash, err := g.r.ResolveRevision(plumbing.Revision(ref))
		if err != nil {
			return nil, fmt.Errorf("resolving rev %s for %s: %w", ref, path, err)
		}
		g.h = *hash
	}
	return &g, nil
}

func (g *GitRepo) Commits() ([]*CommitReference, error) {
	ci, err := g.r.Log(&git.LogOptions{From: g.h})
	if err != nil {
		return nil, fmt.Errorf("commits from ref: %w", err)
	}

	commitRefs := []*CommitReference{}
	if err := ci.ForEach(func(c *object.Commit) error {
		commitRefs = append(commitRefs, &CommitReference{commit: c})
		return nil
	}); err != nil {
		return nil, err
	}

	// new we fetch for possible tags for each commit
	iter, err := g.r.References()
	if err != nil {
		return nil, err
	}

	if err := iter.ForEach(func(ref *plumbing.Reference) error {
		for _, c := range commitRefs {
			obj, err := g.r.TagObject(ref.Hash())
			switch {
			case err == nil:
				if obj.Target == c.commit.Hash {
					c.AddReference(ref)
				}
			case errors.Is(err, plumbing.ErrObjectNotFound):
				if c.commit.Hash == ref.Hash() {
					c.AddReference(ref)
				}
			default:
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return commitRefs, nil
}

func (g *GitRepo) LastCommit() (*CommitReference, error) {
	c, err := g.r.CommitObject(g.h)
	if err != nil {
		return nil, fmt.Errorf("last commit: %w", err)
	}

	iter, err := g.r.Tags()
	if err != nil {
		return nil, err
	}

	commitRef := &CommitReference{commit: c}
	if err := iter.ForEach(func(ref *plumbing.Reference) error {
		obj, err := g.r.TagObject(ref.Hash())
		switch {
		case err == nil:
			if obj.Target == commitRef.commit.Hash {
				commitRef.AddReference(ref)
			}
		case errors.Is(err, plumbing.ErrObjectNotFound):
			if commitRef.commit.Hash == ref.Hash() {
				commitRef.AddReference(ref)
			}
		default:
			return err
		}

		return nil
	}); err != nil {
		return nil, err
	}

	return commitRef, nil
}

func (g *GitRepo) FileContent(path string) (string, error) {
	c, err := g.r.CommitObject(g.h)
	if err != nil {
		return "", fmt.Errorf("commit object: %w", err)
	}

	tree, err := c.Tree()
	if err != nil {
		return "", fmt.Errorf("file tree: %w", err)
	}

	file, err := tree.File(path)
	if err != nil {
		return "", err
	}

	isbin, _ := file.IsBinary()

	if !isbin {
		return file.Contents()
	} else {
		return "Not displaying binary file", nil
	}
}

func (g *GitRepo) Tags() ([]*TagReference, error) {
	iter, err := g.r.Tags()
	if err != nil {
		return nil, fmt.Errorf("tag objects: %w", err)
	}

	tags := make([]*TagReference, 0)

	if err := iter.ForEach(func(ref *plumbing.Reference) error {
		obj, err := g.r.TagObject(ref.Hash())
		switch {
		case err == nil:
			tags = append(tags, &TagReference{
				ref: ref,
				tag: obj,
			})
		case errors.Is(err, plumbing.ErrObjectNotFound):
			tags = append(tags, &TagReference{
				ref: ref,
			})
		default:
			return err
		}
		return nil
	}); err != nil {
		return nil, err
	}

	tagList := &TagList{r: g.r, refs: tags}
	sort.Sort(tagList)
	return tags, nil
}

func (g *GitRepo) Branches() ([]*plumbing.Reference, error) {
	bi, err := g.r.Branches()
	if err != nil {
		return nil, fmt.Errorf("branchs: %w", err)
	}

	branches := []*plumbing.Reference{}

	_ = bi.ForEach(func(ref *plumbing.Reference) error {
		branches = append(branches, ref)
		return nil
	})

	return branches, nil
}

func (g *GitRepo) FindMainBranch(branches []string) (string, error) {
	for _, b := range branches {
		_, err := g.r.ResolveRevision(plumbing.Revision(b))
		if err == nil {
			return b, nil
		}
	}
	// Repositories may use a default branch other than main or master.
	head, err := g.r.Head()
	if err == nil && head.Name().IsBranch() {
		return head.Name().Short(), nil
	}
	return "HEAD", nil
}

// WriteTar writes itself from a tree into a binary tar file format.
// prefix is root folder to be appended.
func (g *GitRepo) WriteTar(w io.Writer, prefix string) error {
	tw := tar.NewWriter(w)
	defer func() { _ = tw.Close() }()

	c, err := g.r.CommitObject(g.h)
	if err != nil {
		return fmt.Errorf("commit object: %w", err)
	}

	tree, err := c.Tree()
	if err != nil {
		return err
	}

	walker := object.NewTreeWalker(tree, true, nil)
	defer walker.Close()

	name, entry, err := walker.Next()
	for ; err == nil; name, entry, err = walker.Next() {
		info, err := newInfoWrapper(name, prefix, &entry, tree)
		if err != nil {
			return err
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}

		err = tw.WriteHeader(header)
		if err != nil {
			return err
		}

		if !info.IsDir() {
			file, err := tree.File(name)
			if err != nil {
				return err
			}

			reader, err := file.Reader()
			if err != nil {
				return err
			}

			_, err = io.Copy(tw, reader)
			if err != nil {
				_ = reader.Close()
				return err
			}
			_ = reader.Close()
		}
	}

	return nil
}

func newInfoWrapper(
	name string,
	prefix string,
	entry *object.TreeEntry,
	tree *object.Tree,
) (*infoWrapper, error) {
	var (
		size  int64
		mode  fs.FileMode
		isDir bool
	)

	if entry.Mode.IsFile() {
		file, err := tree.TreeEntryFile(entry)
		if err != nil {
			return nil, err
		}
		mode = fs.FileMode(file.Mode)

		size, err = tree.Size(name)
		if err != nil {
			return nil, err
		}
	} else {
		isDir = true
		mode = fs.ModeDir | fs.ModePerm
	}

	fullname := path.Join(prefix, name)
	return &infoWrapper{
		name:    fullname,
		size:    size,
		mode:    mode,
		modTime: time.Unix(0, 0),
		isDir:   isDir,
	}, nil
}

func (i *infoWrapper) Name() string {
	return i.name
}

func (i *infoWrapper) Size() int64 {
	return i.size
}

func (i *infoWrapper) Mode() fs.FileMode {
	return i.mode
}

func (i *infoWrapper) ModTime() time.Time {
	return i.modTime
}

func (i *infoWrapper) IsDir() bool {
	return i.isDir
}

func (i *infoWrapper) Sys() any {
	return nil
}

func (t *TagReference) Name() string {
	return t.ref.Name().Short()
}

func (t *TagReference) Message() string {
	if t.tag != nil {
		return t.tag.Message
	}
	return ""
}

func (c *CommitReference) Commit() *object.Commit {
	return c.commit
}

func (c *CommitReference) HasReference() bool {
	return len(c.refs) > 0
}

func (c *CommitReference) References() []*plumbing.Reference {
	return c.refs
}

func (c *CommitReference) AddReference(ref *plumbing.Reference) {
	c.refs = append(c.refs, ref)
}
