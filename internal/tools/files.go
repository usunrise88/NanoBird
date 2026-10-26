package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/usunrise88/nanobird/internal/mech"
	"github.com/usunrise88/nanobird/internal/safety"
)

const (
	// defaultReadLines is how many lines read_file shows when no limit is given.
	defaultReadLines = 2000
	// maxListDepth bounds the depth list_dir will descend.
	maxListDepth = 4
)

var readFileTool = Tool{
	Name:        "read_file",
	Description: "Read a text file in the workspace. Lines are numbered from 1. Use offset and limit to read part of a long file.",
	Parameters: schema(`{
		"path": {"type": "string", "description": "File path inside the workspace"},
		"offset": {"type": "integer", "description": "First line to show, 1-based"},
		"limit": {"type": "integer", "description": "Number of lines to show, 2000 by default"}
	}`, "path"),
	run: runReadFile,
}

func runReadFile(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	var a struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := decode(args, &a); err != nil {
		return "", err
	}
	abs, err := env.resolve(a.Path)
	if err != nil {
		return "", err
	}
	var data []byte
	handled := false
	if env.ReadText != nil {
		text, ok, err := env.ReadText(ctx, abs)
		if err != nil {
			return "", err
		}
		data, handled = []byte(text), ok
	}
	if !handled {
		data, err = os.ReadFile(abs)
		if err != nil {
			return "", err
		}
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return "", fmt.Errorf("%s is binary; read_file shows text only", a.Path)
	}
	text := strings.TrimSuffix(string(data), "\n")
	if text == "" {
		return "(empty file)", nil
	}
	lines := strings.Split(text, "\n")
	first := max(a.Offset, 1)
	if first > len(lines) {
		return "", fmt.Errorf("%s has %d lines; offset %d is past the end", a.Path, len(lines), first)
	}
	limit := a.Limit
	if limit <= 0 {
		limit = defaultReadLines
	}
	last := min(first-1+limit, len(lines))
	var b strings.Builder
	for i := first - 1; i < last; i++ {
		fmt.Fprintf(&b, "%6d\t%s\n", i+1, strings.TrimSuffix(lines[i], "\r"))
	}
	trailer := ""
	if last < len(lines) {
		trailer = fmt.Sprintf("[showing lines %d-%d of %d; use offset to read the rest]\n", first, last, len(lines))
	}
	return env.crawlRead(a.Path, len(data), b.String(), trailer), nil
}

var listDirTool = Tool{
	Name:        "list_dir",
	Description: "List the entries of a directory in the workspace. Directories end with a slash. .git and .nanobird are not shown.",
	Parameters: schema(`{
		"path": {"type": "string", "description": "Directory inside the workspace, \".\" for the root"},
		"depth": {"type": "integer", "description": "How many levels to list, 1 by default"}
	}`),
	run: runListDir,
}

func runListDir(_ context.Context, env *Env, args json.RawMessage) (string, error) {
	var a struct {
		Path  string `json:"path"`
		Depth int    `json:"depth"`
	}
	if err := decode(args, &a); err != nil {
		return "", err
	}
	if a.Path == "" {
		a.Path = "."
	}
	abs, err := env.resolve(a.Path)
	if err != nil {
		return "", err
	}
	rel, err := env.Workspace.Rel(abs)
	if err != nil {
		return "", err
	}
	ig, err := safety.LoadIgnore(env.Workspace.Root())
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := listTree(&b, abs, rel, "", 1, min(max(a.Depth, 1), maxListDepth), ig.Ignored); err != nil {
		return "", err
	}
	if b.Len() == 0 {
		return "(empty directory)", nil
	}
	return b.String(), nil
}

// listTree writes the entries of dir, descending while level is within depth. rel
// is dir's path in the workspace, and skip leaves out the entries the ignore list
// names (SPEC §6.1).
func listTree(b *strings.Builder, dir, rel, prefix string, level, depth int, skip func(rel string, isDir bool) bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		entryRel := path.Join(rel, e.Name())
		if e.Name() == ".git" || e.Name() == ".nanobird" || skip(entryRel, e.IsDir()) {
			continue
		}
		name := prefix + e.Name()
		if !e.IsDir() {
			b.WriteString(name + "\n")
			continue
		}
		b.WriteString(name + "/\n")
		if level < depth {
			if err := listTree(b, filepath.Join(dir, e.Name()), entryRel, name+"/", level+1, depth, skip); err != nil {
				return err
			}
		}
	}
	return nil
}

var createFileTool = Tool{
	Name:        "create_file",
	Description: "Create a new text file in the workspace, with its parent directories. An existing path is refused: change it with edit_file. replace=true overwrites it, keeping a backup.",
	Parameters: schema(`{
		"path": {"type": "string", "description": "File path inside the workspace"},
		"content": {"type": "string", "description": "Full content of the file"},
		"replace": {"type": "boolean", "description": "Overwrite an existing file"}
	}`, "path", "content"),
	run: runCreateFile,
}

func runCreateFile(_ context.Context, env *Env, args json.RawMessage) (string, error) {
	var a struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		Replace bool   `json:"replace"`
	}
	if err := decode(args, &a); err != nil {
		return "", err
	}
	abs, err := env.resolve(a.Path)
	if err != nil {
		return "", err
	}
	existed := false
	if _, err := os.Lstat(abs); err == nil {
		existed = true
		if env.Switches.On(mech.WriteGuard) && !a.Replace {
			return "", fmt.Errorf("%s exists; change it with edit_file, or call create_file with replace=true to overwrite it", a.Path)
		}
		if _, err := env.Workspace.Backup(abs, env.Clock()); err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(abs, []byte(a.Content), 0o644); err != nil {
		return "", err
	}
	if existed {
		return fmt.Sprintf("replaced %s (%d bytes); the previous version is in the backups", a.Path, len(a.Content)), nil
	}
	return fmt.Sprintf("created %s (%d bytes)", a.Path, len(a.Content)), nil
}

var editFileTool = Tool{
	Name:        "edit_file",
	Description: "Replace exact text in an existing file. old must match the file exactly, including indentation. It must match once unless replace_all is true.",
	Parameters: schema(`{
		"path": {"type": "string", "description": "File path inside the workspace"},
		"old": {"type": "string", "description": "Exact text to replace"},
		"new": {"type": "string", "description": "Text that takes its place"},
		"replace_all": {"type": "boolean", "description": "Replace every match, not only a unique one"}
	}`, "path", "old", "new"),
	run: runEditFile,
}

func runEditFile(_ context.Context, env *Env, args json.RawMessage) (string, error) {
	var a struct {
		Path       string `json:"path"`
		Old        string `json:"old"`
		New        string `json:"new"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := decode(args, &a); err != nil {
		return "", err
	}
	if a.Old == "" {
		return "", errors.New("old must not be empty; use create_file for a new file")
	}
	abs, err := env.resolve(a.Path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("%s does not exist; use create_file for a new file", a.Path)
	}
	if err != nil {
		return "", err
	}
	text := string(data)
	old, repl := a.Old, a.New
	if strings.Contains(text, "\r\n") {
		// read_file shows lines without their carriage returns, so the model
		// writes LF; a CRLF file needs the same endings to match.
		old = strings.ReplaceAll(old, "\n", "\r\n")
		repl = strings.ReplaceAll(repl, "\n", "\r\n")
	}
	n := strings.Count(text, old)
	switch {
	case n == 0:
		return "", fmt.Errorf("old text not found in %s (0 matches); copy it exactly from read_file, including indentation", a.Path)
	case n > 1 && !a.ReplaceAll:
		return "", fmt.Errorf("old text matches %d places in %s (lines %s); add surrounding lines to make it unique, or set replace_all", n, a.Path, matchLines(text, old))
	}
	if _, err := env.Workspace.Backup(abs, env.Clock()); err != nil {
		return "", err
	}
	count := 1
	if a.ReplaceAll {
		count = n
	}
	if err := os.WriteFile(abs, []byte(strings.Replace(text, old, repl, count)), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("edited %s: %d replacement(s)", a.Path, count), nil
}

// matchLines lists up to five line numbers where old starts in text.
func matchLines(text, old string) string {
	var nums []string
	for off := 0; len(nums) < 5; {
		i := strings.Index(text[off:], old)
		if i < 0 {
			break
		}
		at := off + i
		nums = append(nums, strconv.Itoa(strings.Count(text[:at], "\n")+1))
		off = at + len(old)
	}
	return strings.Join(nums, ", ")
}

var appendFileTool = Tool{
	Name:        "append_file",
	Description: "Append text to a file, creating it if needed. Use it to write a large file in chunks.",
	Parameters: schema(`{
		"path": {"type": "string", "description": "File path inside the workspace"},
		"content": {"type": "string", "description": "Text to append"}
	}`, "path", "content"),
	run: runAppendFile,
}

func runAppendFile(_ context.Context, env *Env, args json.RawMessage) (string, error) {
	var a struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := decode(args, &a); err != nil {
		return "", err
	}
	abs, err := env.resolve(a.Path)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(abs); err == nil {
		if _, err := env.Workspace.Backup(abs, env.Clock()); err != nil {
			return "", err
		}
	} else if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	f, err := os.OpenFile(abs, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(a.Content); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("appended %d bytes to %s (now %d bytes)", len(a.Content), a.Path, info.Size()), nil
}

var deleteFileTool = Tool{
	Name:        "delete_file",
	Description: "Delete one file. It is moved to .nanobird/trash, so rollback or a manual restore can bring it back.",
	Parameters: schema(`{
		"path": {"type": "string", "description": "File path inside the workspace"}
	}`, "path"),
	run: runDeleteFile,
}

func runDeleteFile(_ context.Context, env *Env, args json.RawMessage) (string, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := decode(args, &a); err != nil {
		return "", err
	}
	abs, err := env.resolve(a.Path)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory; delete_file removes single files", a.Path)
	}
	moved, err := env.Workspace.Trash(abs, env.Clock())
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("moved %s to %s", a.Path, moved), nil
}

var rollbackTool = Tool{
	Name:        "rollback",
	Description: "Restore the last backup of a file. The version it replaces is backed up first, so a rollback can be undone too.",
	Parameters: schema(`{
		"path": {"type": "string", "description": "File path inside the workspace"}
	}`, "path"),
	run: runRollback,
}

func runRollback(_ context.Context, env *Env, args json.RawMessage) (string, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := decode(args, &a); err != nil {
		return "", err
	}
	abs, err := env.resolve(a.Path)
	if err != nil {
		return "", err
	}
	backup, err := env.Workspace.LatestBackup(abs)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(abs); err == nil {
		if _, err := env.Workspace.Backup(abs, env.Clock()); err != nil {
			return "", err
		}
	}
	data, err := os.ReadFile(backup)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(abs, data, 0o644); err != nil {
		return "", err
	}
	from, err := env.Workspace.Rel(backup)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("restored %s from %s", a.Path, from), nil
}
