package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// File databases (SQLite, DuckDB) live on the dataport server. Their paths
// are relative to one directory the operator allows, so a connection can
// never open dataport's own store or any other file of the host.

var files struct {
	mu   sync.RWMutex
	root string
	deny []string
}

var ErrFilesDisabled = errors.New("file databases are disabled; set DATAPORT_QUERY_FILE_DIR on the server")

// ErrFilePath wraps every refusal of a database file path, so it reaches
// the admin as is instead of as an unreachable database.
var ErrFilePath = errors.New("database file")

func pathErr(msg string) error {
	return fmt.Errorf("%w: %s", ErrFilePath, msg)
}

// ConfigureFiles sets the directory file databases are opened from. Empty
// disables them. Paths under deny, such as the data directory, are refused
// even when they are inside root.
func ConfigureFiles(root string, deny ...string) error {
	files.mu.Lock()
	defer files.mu.Unlock()

	files.root, files.deny = "", nil

	if root == "" {
		return nil
	}

	abs, err := realPath(root)
	if err != nil {
		return fmt.Errorf("file database directory %s; %w", root, err)
	}

	files.root = abs

	for _, d := range deny {
		if d == "" {
			continue
		}

		if p, err := realPath(d); err == nil {
			files.deny = append(files.deny, p)
		}
	}

	return nil
}

// FilesEnabled reports whether file databases can be used.
func FilesEnabled() bool {
	files.mu.RLock()
	defer files.mu.RUnlock()

	return files.root != ""
}

// resolveFile turns the path of a connection into an absolute path inside
// the allowed directory. A file that does not exist yet is allowed only when
// create is set.
func resolveFile(name string, create bool) (string, error) {
	files.mu.RLock()
	root, deny := files.root, files.deny
	files.mu.RUnlock()

	if root == "" {
		return "", ErrFilesDisabled
	}

	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, "?#%\x00") {
		return "", pathErr("invalid file name")
	}

	rel := filepath.FromSlash(name)
	if filepath.IsAbs(rel) {
		// The root has its symlinks resolved, so an absolute path is too:
		// on macOS /var leads to /private/var.
		abs := filepath.Clean(rel)
		if real, err := realPath(abs); err == nil {
			abs = real
		} else if dir, err := realPath(filepath.Dir(abs)); err == nil {
			abs = filepath.Join(dir, filepath.Base(abs))
		}

		r, err := filepath.Rel(root, abs)
		if err != nil {
			return "", pathErr("the file must be inside the file database directory")
		}

		rel = r
	}

	if !filepath.IsLocal(rel) {
		return "", pathErr("the file must be inside the file database directory")
	}

	full := filepath.Join(root, rel)

	// Symlinks are followed before the check, so a link inside the
	// directory cannot point outside of it.
	real, err := realPath(full)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) || !create {
			return "", pathErr(name + " does not exist or cannot be read")
		}

		dir, derr := realPath(filepath.Dir(full))
		if derr != nil {
			return "", pathErr("the directory of " + name + " does not exist")
		}

		real = filepath.Join(dir, filepath.Base(full))
	}

	if !within(root, real) {
		return "", pathErr("the file must be inside the file database directory")
	}

	for _, d := range deny {
		if within(d, real) {
			return "", pathErr("the file is in a directory dataport keeps for itself")
		}
	}

	return real, nil
}

func realPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}

	return filepath.EvalSymlinks(abs)
}

func within(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)

	return err == nil && filepath.IsLocal(rel) || p == dir
}
