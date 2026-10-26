package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/usunrise88/nanobird/internal/safety"
)

// maxMatches bounds one search (SPEC §6.1).
const maxMatches = 200

var searchFilesTool = Tool{
	Name:        "search_files",
	Description: "Search the text of the files in the workspace with a regular expression. Each match comes back as path:line: text. .git and .nanobird are skipped, and 200 matches come back at most.",
	Parameters: schema(`{
		"pattern": {"type": "string", "description": "Regular expression, in Go syntax"},
		"path": {"type": "string", "description": "Directory to search, the workspace root by default"},
		"glob": {"type": "string", "description": "File name pattern such as *.go"}
	}`, "pattern"),
	run: runSearchFiles,
}

func runSearchFiles(_ context.Context, env *Env, args json.RawMessage) (string, error) {
	var a struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
		Glob    string `json:"glob"`
	}
	if err := decode(args, &a); err != nil {
		return "", err
	}
	re, err := regexp.Compile(a.Pattern)
	if err != nil {
		return "", fmt.Errorf("pattern: %w", err)
	}
	start := "."
	if a.Path != "" {
		start = a.Path
	}
	root, err := env.resolve(start)
	if err != nil {
		return "", err
	}
	ig, err := safety.LoadIgnore(env.Workspace.Root())
	if err != nil {
		return "", err
	}
	var b strings.Builder
	matches, truncated := 0, false
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			// An unreadable directory is skipped and the walk goes on.
			return filepath.SkipDir
		}
		if d.IsDir() {
			if p != root && (d.Name() == ".git" || d.Name() == ".nanobird" || ignored(env.Workspace, ig, p, true)) {
				return filepath.SkipDir
			}
			return nil
		}
		if ignored(env.Workspace, ig, p, false) {
			return nil
		}
		if a.Glob != "" {
			if ok, _ := filepath.Match(a.Glob, d.Name()); !ok {
				return nil
			}
		}
		rel := strings.TrimPrefix(p, root+string(filepath.Separator))
		if searchFile(p, rel, re, &b, &matches) {
			truncated = true
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if matches == 0 {
		return "no matches", nil
	}
	if truncated {
		fmt.Fprintf(&b, "[stopped at %d matches]\n", maxMatches)
	}
	return b.String(), nil
}

// searchFile writes the matching lines of one file to b and counts them. It
// reports true once the match limit is reached. Unreadable and binary files are
// skipped.
func searchFile(p, rel string, re *regexp.Regexp, b *strings.Builder, matches *int) bool {
	data, readErr := os.ReadFile(p)
	if readErr != nil || bytes.IndexByte(data, 0) >= 0 {
		return false
	}
	for i, line := range strings.Split(string(data), "\n") {
		if !re.MatchString(line) {
			continue
		}
		*matches++
		if *matches > maxMatches {
			return true
		}
		fmt.Fprintf(b, "%s:%d: %s\n", filepath.ToSlash(rel), i+1, strings.TrimRight(line, "\r"))
	}
	return false
}

var runTestsTool = Tool{
	Name:        "run_tests",
	Description: "Run the tests of the workspace with the runner it detects (go, npm, pytest or cargo). The result starts with PASS or FAIL and ends with the output.",
	Parameters:  schema(`{}`),
	run:         runTestsCall,
}

func runTestsCall(ctx context.Context, env *Env, _ json.RawMessage) (string, error) {
	runner, ok := DetectRunner(env.Workspace.Root())
	if !ok {
		return "", errors.New("no test runner applies to this workspace: it needs a go.mod with tests, a package.json with a test script, a tests directory or pytest.ini, or a Cargo.toml")
	}
	passed, out, err := RunTests(ctx, env, runner)
	if err != nil {
		return "", err
	}
	verdict := "FAIL"
	if passed {
		verdict = "PASS"
	}
	return fmt.Sprintf("%s (%s)\n%s", verdict, runner.Name, out), nil
}

// ignored reports whether the ignore list leaves out the path abs (SPEC §6.1).
func ignored(w *safety.Workspace, ig *safety.Ignore, abs string, isDir bool) bool {
	rel, err := w.Rel(abs)
	return err == nil && ig.Ignored(rel, isDir)
}
