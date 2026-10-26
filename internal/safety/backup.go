package safety

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// stamp names backups and trashed files. It sorts by time and has no colons,
// which Windows forbids in file names.
const stamp = "20060102-150405.000000000"

// Backup keeps a copy of the file at abs in .nanobird/backups before a write
// (SPEC §5.6, ring 4). It returns the workspace-relative path of the copy.
func (w *Workspace) Backup(abs string, now time.Time) (string, error) {
	return w.keep(abs, "backups", now, false)
}

// Trash moves the file at abs into .nanobird/trash instead of deleting it
// (SPEC §5.6, ring 4). It returns the workspace-relative path of the moved file.
func (w *Workspace) Trash(abs string, now time.Time) (string, error) {
	return w.keep(abs, "trash", now, true)
}

// LatestBackup returns the absolute path of the newest backup of the file at
// abs.
func (w *Workspace) LatestBackup(abs string) (string, error) {
	rel, err := w.Rel(abs)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(w.root, filepath.FromSlash(path.Join(".nanobird", "backups", path.Dir(rel))))
	prefix := path.Base(rel) + "@"
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("no backup of %s", rel)
	}
	best := ""
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) && e.Name() > best {
			best = e.Name()
		}
	}
	if best == "" {
		return "", fmt.Errorf("no backup of %s", rel)
	}
	return filepath.Join(dir, best), nil
}

// keep copies or moves the file at abs to .nanobird/<bucket>/<rel>@<stamp>.
func (w *Workspace) keep(abs, bucket string, now time.Time, move bool) (string, error) {
	rel, err := w.Rel(abs)
	if err != nil {
		return "", err
	}
	name := rel + "@" + now.UTC().Format(stamp)
	dstRel := path.Join(".nanobird", bucket, name)
	for i := 2; exists(filepath.Join(w.root, filepath.FromSlash(dstRel))); i++ {
		dstRel = fmt.Sprintf("%s.%d", path.Join(".nanobird", bucket, name), i)
	}
	dst := filepath.Join(w.root, filepath.FromSlash(dstRel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	if move {
		err = os.Rename(abs, dst)
	} else {
		err = copyFile(abs, dst)
	}
	if err != nil {
		return "", err
	}
	return dstRel, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}
