package handlers

import (
	"html/template"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// templateCache parses template files once and keeps the result. On every
// use the files are checked with a cheap os.Stat: if one changed, the set
// is parsed again, and if one is missing the error wraps fs.ErrNotExist,
// which the handlers turn into a 404. Edits are picked up without a
// restart, but a page is not re-parsed on every request.
type templateCache struct {
	dir string

	mu   sync.Mutex
	sets map[string]cachedTemplate
}

type cachedTemplate struct {
	tmpl   *template.Template
	stamps []fileStamp
}

// fileStamp identifies one version of a file.
type fileStamp struct {
	modTime int64 // nanoseconds since 1970
	size    int64
}

func newTemplateCache(dir string) *templateCache {
	return &templateCache{dir: dir, sets: make(map[string]cachedTemplate)}
}

// get returns the template set parsed from files (relative to the cache
// directory). The first file is the set's main template.
func (tc *templateCache) get(files ...string) (*template.Template, error) {
	paths := make([]string, len(files))
	stamps := make([]fileStamp, len(files))
	for i, name := range files {
		paths[i] = filepath.Join(tc.dir, name)
		info, err := os.Stat(paths[i])
		if err != nil {
			return nil, err // wraps fs.ErrNotExist when the file is missing
		}
		stamps[i] = fileStamp{modTime: info.ModTime().UnixNano(), size: info.Size()}
	}

	key := strings.Join(files, "+")
	tc.mu.Lock()
	cached, ok := tc.sets[key]
	tc.mu.Unlock()
	if ok && slices.Equal(cached.stamps, stamps) {
		return cached.tmpl, nil // a parsed template is safe for concurrent use
	}

	tmpl, err := template.ParseFiles(paths...)
	if err != nil {
		return nil, err
	}

	tc.mu.Lock()
	tc.sets[key] = cachedTemplate{tmpl: tmpl, stamps: stamps}
	tc.mu.Unlock()
	return tmpl, nil
}
