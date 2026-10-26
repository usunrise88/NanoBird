package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// prepareWorkdir copies the fixtures of a task into a new workdir (SPEC §10.1). A file
// lands in the workdir root under its own name. A folder is copied into the workdir
// under its own name, as Mingbird's runner copies them.
func prepareWorkdir(root string, fixtures []string, workdir string) error {
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		return err
	}
	for _, rel := range fixtures {
		src := filepath.Join(root, rel)
		info, err := os.Stat(src)
		if err != nil {
			return fmt.Errorf("fixture %s: %w", rel, err)
		}
		dst := filepath.Join(workdir, filepath.Base(src))
		if info.IsDir() {
			err = copyTree(src, dst)
		} else {
			err = copyFile(src, dst)
		}
		if err != nil {
			return fmt.Errorf("fixture %s: %w", rel, err)
		}
	}
	return nil
}

// copyTree copies the folder src to dst, which may exist already.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

// copyFile copies one file, and closes both files whatever happens.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
