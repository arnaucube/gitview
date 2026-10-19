package routes

import (
	"compress/gzip"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	securejoin "github.com/cyphar/filepath-securejoin"
	"github.com/dustin/go-humanize"
	"github.com/microcosm-cc/bluemonday"
	"github.com/russross/blackfriday/v2"
	"gitview/internal/config"
	"gitview/internal/git"
)

type deps struct {
	c             *config.Config
	catalog       *repositoryCatalog
	indexTemplate *template.Template
}

func (d *deps) Index(w http.ResponseWriter, r *http.Request) {
	infos, err := d.catalog.snapshot(r.Context())
	if err != nil {
		if r.Context().Err() == nil {
			d.Write500(w)
		}
		return
	}

	total := len(infos)
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query != "" {
		needle := strings.ToLower(query)
		matches := make([]repositoryInfo, 0, len(infos))
		for _, repo := range infos {
			if strings.Contains(strings.ToLower(repo.Name+" "+repo.Desc), needle) {
				matches = append(matches, repo)
			}
		}
		infos = matches
	}
	sortMode := "pinned"
	switch r.URL.Query().Get("sort") {
	case "name", "updated":
		sortMode = r.URL.Query().Get("sort")
	}

	// Pin configured repositories first; duplicates keep their first listed position.
	pinned := make(map[string]int, len(d.c.Repo.Pinned))
	for i, name := range d.c.Repo.Pinned {
		name = getDisplayName(name)
		if _, exists := pinned[name]; !exists {
			pinned[name] = i
		}
	}
	sort.SliceStable(infos, func(i, j int) bool {
		if sortMode == "name" {
			return strings.ToLower(infos[i].DisplayName) < strings.ToLower(infos[j].DisplayName)
		}
		if sortMode == "updated" {
			return infos[j].updatedAt.Before(infos[i].updatedAt)
		}
		iRank, iPinned := pinned[infos[i].DisplayName]
		jRank, jPinned := pinned[infos[j].DisplayName]
		if iPinned != jPinned {
			return iPinned
		}
		if iPinned && iRank != jRank {
			return iRank < jRank
		}
		return infos[j].updatedAt.Before(infos[i].updatedAt)
	})

	const pageSize = 10
	filtered := len(infos)
	pages := max(1, (filtered+pageSize-1)/pageSize)
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1
	}
	page = min(page, pages)
	start := (page - 1) * pageSize
	end := min(start+pageSize, filtered)
	infos = infos[start:end]
	// Only first visits to a commit need source analysis; later visits reuse its results.
	for i := range infos {
		infos[i].Idle = humanize.Time(infos[i].updatedAt)
		infos[i].Languages, err = infos[i].languageStats()
		if err != nil {
			log.Printf("languages for %s: %v", infos[i].Name, err)
		}
	}
	pageURL := func(number int) string {
		values := url.Values{"page": {strconv.Itoa(number)}}
		if query != "" {
			values.Set("q", query)
		}
		if sortMode != "pinned" {
			values.Set("sort", sortMode)
		}
		return "/?" + values.Encode()
	}

	data := make(map[string]interface{})
	data["meta"] = d.c.Meta
	data["info"] = infos
	data["total"] = total
	data["filtered"] = filtered
	data["query"] = query
	data["sort"] = sortMode
	data["page"] = page
	data["pages"] = pages
	data["start"] = start + 1
	data["end"] = end
	if page > 1 {
		data["prevURL"] = pageURL(page - 1)
	}
	if page < pages {
		data["nextURL"] = pageURL(page + 1)
	}
	data["pinned"] = len(pinned) > 0

	if err := d.indexTemplate.ExecuteTemplate(w, "index", data); err != nil {
		log.Println(err)
		return
	}
}

func (d *deps) RepoIndex(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if d.isIgnored(name) {
		d.Write404(w)
		return
	}
	name = filepath.Clean(name)
	path, err := securejoin.SecureJoin(d.c.Repo.ScanPath, name)
	if err != nil {
		log.Printf("securejoin error: %v", err)
		d.Write404(w)
		return
	}

	gr, err := git.Open(path, "")
	if err != nil {
		d.Write404(w)
		return
	}

	mainBranch, err := gr.FindMainBranch(d.c.Repo.MainBranch)
	if err != nil {
		d.Write500(w)
		log.Println(err)
		return
	}

	// Resolve the selected default branch before reading its files and history.
	gr, err = git.Open(path, mainBranch)
	if err != nil {
		d.Write404(w)
		return
	}

	commits, err := gr.Commits()
	if err != nil {
		d.Write500(w)
		log.Println(err)
		return
	}

	var readmeContent template.HTML
	for _, readme := range d.c.Repo.Readme {
		ext := filepath.Ext(readme)
		content, _ := gr.FileContent(readme)
		if len(content) > 0 {
			switch ext {
			case ".md", ".mkd", ".markdown":
				unsafe := blackfriday.Run(
					[]byte(content),
					blackfriday.WithExtensions(blackfriday.CommonExtensions),
				)
				html := bluemonday.UGCPolicy().SanitizeBytes(unsafe)
				readmeContent = template.HTML(html)
			default:
				safe := bluemonday.UGCPolicy().SanitizeBytes([]byte(content))
				readmeContent = template.HTML(
					fmt.Sprintf(`<pre>%s</pre>`, safe),
				)
			}
			break
		}
	}

	if readmeContent == "" {
		log.Printf("no readme found for %s", name)
	}

	tpath := filepath.Join(d.c.Dirs.Templates, "*")
	t := template.Must(template.ParseGlob(tpath))

	files, err := gr.FileTree("")
	if err != nil {
		d.Write500(w)
		return
	}
	branches, _ := gr.Branches()
	tags, _ := gr.Tags()

	data := make(map[string]any)
	data["name"] = name
	data["displayname"] = getDisplayName(name)
	data["ref"] = mainBranch
	data["readme"] = readmeContent
	data["commits"] = commits
	data["desc"] = getDescription(path)
	data["servername"] = d.c.Server.Name
	data["meta"] = d.c.Meta
	data["gomod"] = isGoModule(gr)
	data["files"] = files
	data["branches"] = branches
	data["tags"] = tags
	data["commitcount"] = len(commits)
	data["languages"], err = gr.Languages()
	if err != nil {
		log.Printf("languages for %s: %v", name, err)
	}

	if err := t.ExecuteTemplate(w, "repo", data); err != nil {
		log.Println(err)
		return
	}
}

func (d *deps) RepoTree(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if d.isIgnored(name) {
		d.Write404(w)
		return
	}
	treePath := r.PathValue("rest")
	ref := r.PathValue("ref")

	name = filepath.Clean(name)
	path, err := securejoin.SecureJoin(d.c.Repo.ScanPath, name)
	if err != nil {
		log.Printf("securejoin error: %v", err)
		d.Write404(w)
		return
	}
	gr, err := git.Open(path, ref)
	if err != nil {
		d.Write404(w)
		return
	}

	files, err := gr.FileTree(treePath)
	if err != nil {
		d.Write404(w)
		return
	}

	data := make(map[string]any)
	data["name"] = name
	data["displayname"] = getDisplayName(name)
	data["ref"] = ref
	data["parent"] = treePath
	data["desc"] = getDescription(path)
	data["dotdot"] = filepath.Dir(treePath)
	if data["dotdot"] == "." {
		data["dotdot"] = ""
	}
	data["branches"], _ = gr.Branches()
	data["commits"], _ = gr.Commits()

	d.listFiles(files, data, w)
}

func (d *deps) FileContent(w http.ResponseWriter, r *http.Request) {
	var raw bool
	if rawParam, err := strconv.ParseBool(r.URL.Query().Get("raw")); err == nil {
		raw = rawParam
	}

	name := r.PathValue("name")
	if d.isIgnored(name) {
		d.Write404(w)
		return
	}
	treePath := r.PathValue("rest")
	ref := r.PathValue("ref")

	name = filepath.Clean(name)
	path, err := securejoin.SecureJoin(d.c.Repo.ScanPath, name)
	if err != nil {
		log.Printf("securejoin error: %v", err)
		d.Write404(w)
		return
	}

	gr, err := git.Open(path, ref)
	if err != nil {
		d.Write404(w)
		return
	}

	contents, err := gr.FileContent(treePath)
	if err != nil {
		d.Write404(w)
		return
	}
	data := make(map[string]any)
	data["name"] = name
	data["displayname"] = getDisplayName(name)
	data["ref"] = ref
	data["desc"] = getDescription(path)
	data["path"] = treePath

	if raw {
		d.showRaw(contents, w)
	} else {
		if d.c.Meta.SyntaxHighlight == "" {
			d.showFile(contents, data, w)
		} else {
			d.showFileWithHighlight(treePath, contents, data, w)
		}
	}
}

func (d *deps) Archive(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if d.isIgnored(name) {
		d.Write404(w)
		return
	}

	file := r.PathValue("file")

	// TODO: extend this to add more files compression (e.g.: xz)
	if !strings.HasSuffix(file, ".tar.gz") {
		d.Write404(w)
		return
	}

	ref := strings.TrimSuffix(file, ".tar.gz")

	// This allows the browser to use a proper name for the file when
	// downloading
	filename := fmt.Sprintf("%s-%s.tar.gz", name, ref)
	setContentDisposition(w, filename)
	setGZipMIME(w)

	path, err := securejoin.SecureJoin(d.c.Repo.ScanPath, name)
	if err != nil {
		log.Printf("securejoin error: %v", err)
		d.Write404(w)
		return
	}

	gr, err := git.Open(path, ref)
	if err != nil {
		d.Write404(w)
		return
	}

	gw := gzip.NewWriter(w)
	defer func() { _ = gw.Close() }()

	prefix := fmt.Sprintf("%s-%s", name, ref)
	err = gr.WriteTar(gw, prefix)
	if err != nil {
		// once we start writing to the body we can't report error anymore
		// so we are only left with printing the error.
		log.Println(err)
		return
	}

	err = gw.Flush()
	if err != nil {
		// once we start writing to the body we can't report error anymore
		// so we are only left with printing the error.
		log.Println(err)
		return
	}
}

func (d *deps) Log(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if d.isIgnored(name) {
		d.Write404(w)
		return
	}
	ref := r.PathValue("ref")

	path, err := securejoin.SecureJoin(d.c.Repo.ScanPath, name)
	if err != nil {
		log.Printf("securejoin error: %v", err)
		d.Write404(w)
		return
	}

	gr, err := git.Open(path, ref)
	if err != nil {
		d.Write404(w)
		return
	}

	commits, err := gr.Commits()
	if err != nil {
		d.Write500(w)
		log.Println(err)
		return
	}

	tpath := filepath.Join(d.c.Dirs.Templates, "*")
	t := template.Must(template.ParseGlob(tpath))

	data := make(map[string]interface{})
	data["commits"] = commits
	data["meta"] = d.c.Meta
	data["name"] = name
	data["displayname"] = getDisplayName(name)
	data["ref"] = ref
	data["desc"] = getDescription(path)
	data["log"] = true

	if err := t.ExecuteTemplate(w, "log", data); err != nil {
		log.Println(err)
		return
	}
}

func (d *deps) Diff(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if d.isIgnored(name) {
		d.Write404(w)
		return
	}
	ref := r.PathValue("ref")

	path, err := securejoin.SecureJoin(d.c.Repo.ScanPath, name)
	if err != nil {
		log.Printf("securejoin error: %v", err)
		d.Write404(w)
		return
	}
	gr, err := git.Open(path, ref)
	if err != nil {
		d.Write404(w)
		return
	}

	diff, err := gr.Diff()
	if err != nil {
		d.Write500(w)
		log.Println(err)
		return
	}

	tpath := filepath.Join(d.c.Dirs.Templates, "*")
	t := template.Must(template.ParseGlob(tpath))

	data := make(map[string]interface{})

	data["commit"] = diff.Commit
	data["stat"] = diff.Stat
	data["diff"] = diff.Diff
	data["meta"] = d.c.Meta
	data["name"] = name
	data["displayname"] = getDisplayName(name)
	data["ref"] = ref
	data["desc"] = getDescription(path)

	if err := t.ExecuteTemplate(w, "commit", data); err != nil {
		log.Println(err)
		return
	}
}

func (d *deps) Refs(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if d.isIgnored(name) {
		d.Write404(w)
		return
	}

	path, err := securejoin.SecureJoin(d.c.Repo.ScanPath, name)
	if err != nil {
		log.Printf("securejoin error: %v", err)
		d.Write404(w)
		return
	}

	gr, err := git.Open(path, "")
	if err != nil {
		d.Write404(w)
		return
	}

	tags, err := gr.Tags()
	if err != nil {
		// Non-fatal, we *should* have at least one branch to show.
		log.Println(err)
	}

	branches, err := gr.Branches()
	if err != nil {
		log.Println(err)
		d.Write500(w)
		return
	}

	tpath := filepath.Join(d.c.Dirs.Templates, "*")
	t := template.Must(template.ParseGlob(tpath))

	data := make(map[string]interface{})

	data["meta"] = d.c.Meta
	data["name"] = name
	data["displayname"] = getDisplayName(name)
	data["branches"] = branches
	data["tags"] = tags
	data["isrefs"] = true
	data["desc"] = getDescription(path)

	if err := t.ExecuteTemplate(w, "refs", data); err != nil {
		log.Println(err)
		return
	}
}

func (d *deps) ServeStatic(w http.ResponseWriter, r *http.Request) {
	f := r.PathValue("file")
	f = filepath.Clean(f)
	f, err := securejoin.SecureJoin(d.c.Dirs.Static, f)
	if err != nil {
		d.Write404(w)
		return
	}

	http.ServeFile(w, r, f)
}
