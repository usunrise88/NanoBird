package safety

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Workspace is the directory a run may change. Its root is absolute and has
// its symlinks resolved, so every later comparison is between real paths.
type Workspace struct {
	root string
}

// NewWorkspace resolves dir to the workspace root. dir must be an existing
// directory.
func NewWorkspace(dir string) (*Workspace, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("workspace %s: %w", dir, err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("workspace %s: %w", dir, err)
	}
	info, err := os.Stat(real)
	if err != nil {
		return nil, fmt.Errorf("workspace %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace %s is not a directory", dir)
	}
	return &Workspace{root: real}, nil
}

// Root is the absolute, symlink-free workspace directory.
func (w *Workspace) Root() string {
	return w.root
}

// Resolve maps a path the model wrote to a real path inside the workspace.
// Absolute paths and paths that leave the workspace are refused. The part of
// the path that exists is resolved through symlinks, so a link cannot lead
// out; the part that does not exist yet is appended as written.
func (w *Workspace) Resolve(p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", errors.New("path is empty")
	}
	if filepath.IsAbs(p) || strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) || filepath.VolumeName(p) != "" {
		return "", fmt.Errorf("%s: absolute paths are refused, use a path inside the workspace", p)
	}
	joined := filepath.Join(w.root, filepath.FromSlash(p))
	if !within(w.root, joined) {
		return "", fmt.Errorf("%s: outside the workspace", p)
	}
	real, err := resolveExisting(joined)
	if err != nil {
		return "", fmt.Errorf("%s: %w", p, err)
	}
	if !within(w.root, real) {
		return "", fmt.Errorf("%s: resolves outside the workspace", p)
	}
	return real, nil
}

// Rel returns the slash-separated path of abs relative to the workspace root.
func (w *Workspace) Rel(abs string) (string, error) {
	if !within(w.root, abs) {
		return "", fmt.Errorf("%s is outside the workspace", abs)
	}
	rel, err := filepath.Rel(w.root, abs)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}

// within reports whether p lies inside root or is root itself. Both are
// absolute and clean. filepath.Rel compares names case-insensitively on
// Windows.
func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolveExisting resolves symlinks in the longest existing prefix of p and
// appends the rest unchanged. A link that exists but cannot be resolved, such
// as a dangling one, is an error: it could point anywhere.
func resolveExisting(p string) (string, error) {
	var rest []string
	cur := p
	for {
		real, err := filepath.EvalSymlinks(cur)
		if err == nil {
			return filepath.Join(append([]string{real}, rest...)...), nil
		}
		if _, lerr := os.Lstat(cur); lerr == nil || !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", err
		}
		rest = append([]string{filepath.Base(cur)}, rest...)
		cur = parent
	}
}
