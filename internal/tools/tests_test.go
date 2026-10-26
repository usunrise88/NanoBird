package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestDetectRunner(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"empty", map[string]string{"a.txt": "x"}, ""},
		{"go with a test", map[string]string{"go.mod": "module m", "pkg/x_test.go": "package x"}, "go"},
		{"go without tests", map[string]string{"go.mod": "module m", "x.go": "package m"}, ""},
		{"npm", map[string]string{"package.json": `{"scripts":{"test":"jest"}}`}, "npm"},
		{"npm placeholder", map[string]string{"package.json": `{"scripts":{"test":"echo \"Error: no test specified\" && exit 1"}}`}, ""},
		{"pytest", map[string]string{"tests/test_a.py": "def test_a(): pass"}, "pytest"},
		{"cargo", map[string]string{"Cargo.toml": "[package]"}, "cargo"},
	}
	for _, c := range cases {
		r, ok := DetectRunner(writeTree(t, c.files))
		got := ""
		if ok {
			got = r.Name
		}
		if got != c.want {
			t.Errorf("%s: runner %q, want %q", c.name, got, c.want)
		}
	}
}

func TestMissingRunnerIsAnError(t *testing.T) {
	env := newEnv(t)
	if _, _, err := RunTests(context.Background(), env, Runner{Name: "none", Argv: []string{"nanobird-no-such-runner"}}); err == nil {
		t.Fatal("a runner that does not exist was reported as a test result")
	}
}

func TestRunShellRefusesCredentialLocations(t *testing.T) {
	env := newEnv(t)
	if _, err := call(t, env, "run_shell", `{"command":"cat ~/.ssh/id_rsa"}`); err == nil {
		t.Fatal("a credential read was run")
	}
}
