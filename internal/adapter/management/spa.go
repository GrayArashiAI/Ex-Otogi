package management

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// spa serves the built control panel: its files, and index.html for every
// other path, where the app's router takes over. Hashed assets are cached
// for good; index.html never, so a deploy takes effect at once.
func spa(files fs.FS) http.Handler {
	if files == nil {
		files = emptyFS{}
	}
	server := http.FileServerFS(files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if info, err := fs.Stat(files, name); name != "" && err == nil && !info.IsDir() {
			if strings.HasPrefix(name, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			server.ServeHTTP(w, r)
			return
		}
		index, err := fs.ReadFile(files, "index.html")
		if err != nil {
			http.Error(w, "The control panel is not in this build: build web/ (make web) before the binary.", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index) // the client is gone if this fails
	})
}

// emptyFS is a panel that was not built.
type emptyFS struct{}

func (emptyFS) Open(name string) (fs.File, error) {
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}
