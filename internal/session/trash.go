package session

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// trashStamp names trashed sessions. It sorts by time and has no colons, which
// Windows forbids in file names.
const trashStamp = "20060102-150405.000000000"

// Trash moves a session's transcript and its log into .nanobird/trash, so that a
// deleted session can still be recovered (SPEC §8.2, §5.6). The session must not be
// open, since its files are in use. The id must name one session, not a path.
func Trash(root, id string, now time.Time) error {
	if !IsName(id) {
		return fmt.Errorf("session id %q is not a name", id)
	}
	dir := filepath.Join(root, ".nanobird", "sessions", id)
	if _, err := os.Stat(dir); err != nil {
		return err
	}
	stamp := now.UTC().Format(trashStamp)
	dst := filepath.Join(root, ".nanobird", "trash", "sessions", id+"@"+stamp)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(dir, dst); err != nil {
		return err
	}
	logFile := filepath.Join(root, ".nanobird", "logs", id+".log")
	if _, err := os.Stat(logFile); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	logDst := filepath.Join(root, ".nanobird", "trash", "logs", id+"@"+stamp+".log")
	if err := os.MkdirAll(filepath.Dir(logDst), 0o755); err != nil {
		return err
	}
	return os.Rename(logFile, logDst)
}

// IsName reports whether a session id is a plain name, which cannot leave its folder.
func IsName(id string) bool {
	return id != "" && filepath.Base(id) == id && id != "." && id != ".."
}
