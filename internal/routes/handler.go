package routes

import (
	"net/http"

	"gitview/internal/config"
)

// Checks for gitprotocol-http(5) specific smells; if found, passes
// the request on to the git http service, else render the web frontend.
func (d *deps) Multiplex(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("rest")

	if r.URL.Query().Get("service") == "git-receive-pack" || path == "git-receive-pack" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("no pushing allowed!"))
		return
	}

	switch {
	case path == "info/refs" &&
		r.URL.Query().Get("service") == "git-upload-pack" &&
		r.Method == http.MethodGet:
		d.InfoRefs(w, r)
	case path == "git-upload-pack" && r.Method == http.MethodPost:
		d.UploadPack(w, r)
	case r.Method == http.MethodGet && path == "":
		d.RepoIndex(w, r)
	default:
		d.Write404(w)
	}
}

func Handlers(c *config.Config) *http.ServeMux {
	mux := http.NewServeMux()
	d := deps{c}

	mux.HandleFunc("GET /{$}", d.Index)
	mux.HandleFunc("GET /{name}", d.Multiplex)
	mux.HandleFunc("POST /{name}", d.Multiplex)
	mux.HandleFunc("GET /{name}/tree/{ref}/{rest...}", d.RepoTree)
	mux.HandleFunc("GET /{name}/blob/{ref}/{rest...}", d.FileContent)
	mux.HandleFunc("GET /{name}/log/{ref}", d.Log)
	mux.HandleFunc("GET /{name}/archive/{file}", d.Archive)
	mux.HandleFunc("GET /{name}/commit/{ref}", d.Diff)
	mux.HandleFunc("GET /{name}/refs/{$}", d.Refs)
	mux.HandleFunc("GET /{name}/{rest...}", d.Multiplex)
	mux.HandleFunc("POST /{name}/{rest...}", d.Multiplex)

	// Route static paths separately so nested assets cannot conflict with repository routes.
	handler := http.NewServeMux()
	handler.HandleFunc("GET /static/{file...}", d.ServeStatic)
	handler.Handle("/", mux)
	return handler
}
