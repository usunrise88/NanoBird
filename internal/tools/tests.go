package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// testTimeout bounds one run of the test guard (SPEC §5.3).
const testTimeout = 5 * time.Minute

// maxTestOutput bounds the output a failed test run hands back (M3c).
const maxTestOutput = 4096

// Runner is the test command of a workspace.
type Runner struct {
	Name string
	Argv []string
}

// DetectRunner finds the test command of the workspace (SPEC §5.3, the test
// guard). go applies when there is a go.mod and a _test.go file, npm when
// package.json has a test script, pytest when there is a tests directory or a
// pytest.ini, and cargo when there is a Cargo.toml. It returns false when none
// applies.
func DetectRunner(root string) (Runner, bool) {
	if exists(root, "go.mod") && hasFileSuffix(root, "_test.go") {
		return Runner{Name: "go", Argv: []string{"go", "test", "./..."}}, true
	}
	if npmHasTests(root) {
		return Runner{Name: "npm", Argv: []string{"npm", "test"}}, true
	}
	if exists(root, "tests") || exists(root, "pytest.ini") {
		return Runner{Name: "pytest", Argv: []string{"python", "-m", "pytest", "-q"}}, true
	}
	if exists(root, "Cargo.toml") {
		return Runner{Name: "cargo", Argv: []string{"cargo", "test"}}, true
	}
	return Runner{}, false
}

// RunTests runs the runner in the workspace root. It reports whether the run
// passed, with the end of its output. An error means the runner did not start.
func RunTests(ctx context.Context, env *Env, r Runner) (bool, string, error) {
	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.Argv[0], r.Argv[1:]...)
	cmd.Dir = env.Workspace.Root()
	// A child that keeps the pipe open must not hold the run after the runner is stopped.
	cmd.WaitDelay = 2 * time.Second
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	text := tailOf(strings.ToValidUTF8(out.String(), "\uFFFD"))
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return false, text + "\n(the test run timed out)", nil
	}
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		return false, text, nil
	case err != nil:
		return false, "", err
	}
	return true, text, nil
}

// tailOf keeps the last maxTestOutput bytes, starting at a line.
func tailOf(s string) string {
	if len(s) <= maxTestOutput {
		return s
	}
	s = s[len(s)-maxTestOutput:]
	if i := strings.Index(s, "\n"); i >= 0 {
		s = s[i+1:]
	}
	return s
}

func exists(root, name string) bool {
	_, err := os.Stat(filepath.Join(root, name))
	return err == nil
}

// hasFileSuffix reports whether the tree under root holds a file with the
// suffix. It skips the directories that never hold the project's own tests.
func hasFileSuffix(root, suffix string) bool {
	found := false
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable directory is skipped and the walk goes on.
			return filepath.SkipDir
		}
		if d.IsDir() && p != root {
			switch d.Name() {
			case ".git", ".nanobird", "node_modules", "vendor":
				return filepath.SkipDir
			}
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), suffix) {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// npmHasTests reports whether package.json has a real test script.
func npmHasTests(root string) bool {
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return false
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return false
	}
	script := strings.TrimSpace(pkg.Scripts["test"])
	return script != "" && !strings.Contains(script, "no test specified")
}
