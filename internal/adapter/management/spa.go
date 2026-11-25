package management

import (
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/vearutop/statigz"
	"github.com/vearutop/statigz/brotli"
)

// spa serves the built control panel: its files, and index.html for every
// other path, where the app's router takes over. Hashed assets are cached
// for good; index.html never, so a deploy takes effect at once. A file the
// build compressed beside it (.br, .gz) is sent as that to a browser that
// accepts it; nothing is compressed while serving.
func spa(files fs.ReadDirFS) http.Handler {
	if files == nil {
		files = emptyFS{}
	}
	server := statigz.FileServer(files, brotli.AddEncoding, statigz.OnNotFound(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Del("Cache-Control")
		http.Error(w, "The control panel is not in this build: build web/ (make web) before the binary.", http.StatusServiceUnavailable)
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if info, err := fs.Stat(files, name); name != "" && err == nil && !info.IsDir() {
			if strings.HasPrefix(name, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			server.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		index := r.Clone(r.Context())
		index.URL.Path, index.URL.RawPath = "/", "" // statigz serves index.html for the root
		server.ServeHTTP(w, index)
	})
}

// emptyFS is a panel that was not built.
type emptyFS struct{}

func (emptyFS) Open(name string) (fs.File, error) {
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

func (emptyFS) ReadDir(string) ([]fs.DirEntry, error) { return nil, nil }
