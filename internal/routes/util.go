package routes

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gitview/internal/git"
)

func isGoModule(gr *git.GitRepo) bool {
	_, err := gr.FileContent("go.mod")
	return err == nil
}

func getDisplayName(name string) string {
	return strings.TrimSuffix(name, ".git")
}

func getDescription(path string) (desc string) {
	db, err := os.ReadFile(filepath.Join(path, "description"))
	if err == nil {
		desc = string(db)
	} else {
		desc = ""
	}
	return
}

func (d *deps) isUnlisted(name string) bool {
	for _, i := range d.c.Repo.Unlisted {
		if name == i {
			return true
		}
	}

	return false
}

func (d *deps) isIgnored(name string) bool {
	for _, i := range d.c.Repo.Ignore {
		if name == i {
			return true
		}
	}

	return false
}

func setContentDisposition(w http.ResponseWriter, name string) {
	h := "inline; filename=\"" + name + "\""
	w.Header().Add("Content-Disposition", h)
}

func setGZipMIME(w http.ResponseWriter) {
	setMIME(w, "application/gzip")
}

func setMIME(w http.ResponseWriter, mime string) {
	w.Header().Add("Content-Type", mime)
}
