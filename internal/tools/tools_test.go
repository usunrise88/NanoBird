package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/usunrise88/nanobird/internal/mech"
	"github.com/usunrise88/nanobird/internal/safety"
)

func newEnv(t *testing.T) *Env {
	t.Helper()
	ws, err := safety.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return &Env{
		Workspace:    ws,
		Shell:        DetectShell(),
		ShellTimeout: 30 * time.Second,
		Clock:        func() time.Time { now = now.Add(time.Second); return now },
	}
}

// call runs one built-in tool by name with JSON arguments.
func call(t *testing.T, env *Env, name, args string) (string, error) {
	t.Helper()
	tool, ok := NewRegistry(Builtins()).Lookup(name)
	if !ok {
		t.Fatalf("no tool named %s", name)
	}
	return tool.Run(context.Background(), env, json.RawMessage(args))
}

func workspaceFile(t *testing.T, env *Env, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(env.Workspace.Root(), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

func TestSchemasAreJSONObjects(t *testing.T) {
	reg := NewRegistry(Builtins())
	for _, want := range []string{"read_file", "list_dir", "create_file", "edit_file", "append_file", "delete_file", "rollback", "run_shell", "todo", "finish"} {
		if _, ok := reg.Lookup(want); !ok {
			t.Errorf("tool %s is missing", want)
		}
	}
	for _, s := range reg.Schemas() {
		var obj struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(s.Parameters, &obj); err != nil || obj.Type != "object" {
			t.Errorf("%s: schema %s is not an object schema", s.Name, s.Parameters)
		}
	}
}

func TestCreateReadEditAndRollback(t *testing.T) {
	env := newEnv(t)
	if _, err := call(t, env, "create_file", `{"path":"src/a.txt","content":"one\ntwo\nthree\n"}`); err != nil {
		t.Fatalf("create_file: %v", err)
	}
	out, err := call(t, env, "read_file", `{"path":"src/a.txt"}`)
	if err != nil || !strings.Contains(out, "     2\ttwo\n") {
		t.Fatalf("read_file = %q, %v", out, err)
	}
	if _, err := call(t, env, "create_file", `{"path":"src/a.txt","content":"x"}`); err == nil || !strings.Contains(err.Error(), "edit_file") {
		t.Fatalf("create_file over an existing file: %v", err)
	}
	if _, err := call(t, env, "edit_file", `{"path":"src/a.txt","old":"two","new":"TWO"}`); err != nil {
		t.Fatalf("edit_file: %v", err)
	}
	if got := workspaceFile(t, env, "src/a.txt"); got != "one\nTWO\nthree\n" {
		t.Fatalf("after edit: %q", got)
	}
	if _, err := call(t, env, "rollback", `{"path":"src/a.txt"}`); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if got := workspaceFile(t, env, "src/a.txt"); got != "one\ntwo\nthree\n" {
		t.Fatalf("after rollback: %q", got)
	}
}

func TestEditNeedsAUniqueMatchUnlessReplaceAll(t *testing.T) {
	env := newEnv(t)
	if _, err := call(t, env, "create_file", `{"path":"dup.txt","content":"a\nb\na\n"}`); err != nil {
		t.Fatal(err)
	}
	_, err := call(t, env, "edit_file", `{"path":"dup.txt","old":"a","new":"x"}`)
	if err == nil || !strings.Contains(err.Error(), "matches 2 places") || !strings.Contains(err.Error(), "lines 1, 3") {
		t.Fatalf("ambiguous edit: %v", err)
	}
	if _, err := call(t, env, "edit_file", `{"path":"dup.txt","old":"zzz","new":"x"}`); err == nil || !strings.Contains(err.Error(), "0 matches") {
		t.Fatalf("missing text: %v", err)
	}
	if _, err := call(t, env, "edit_file", `{"path":"dup.txt","old":"a","new":"x","replace_all":true}`); err != nil {
		t.Fatalf("replace_all: %v", err)
	}
	if got := workspaceFile(t, env, "dup.txt"); got != "x\nb\nx\n" {
		t.Fatalf("after replace_all: %q", got)
	}
}

func TestEditMatchesCRLFFiles(t *testing.T) {
	env := newEnv(t)
	if _, err := call(t, env, "create_file", `{"path":"win.txt","content":"one\r\ntwo\r\n"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, env, "edit_file", `{"path":"win.txt","old":"one\ntwo","new":"1\n2"}`); err != nil {
		t.Fatalf("edit across CRLF lines: %v", err)
	}
	if got := workspaceFile(t, env, "win.txt"); got != "1\r\n2\r\n" {
		t.Fatalf("after edit: %q", got)
	}
}

func TestWriteGuardSwitchAllowsOverwrite(t *testing.T) {
	env := newEnv(t)
	if _, err := call(t, env, "create_file", `{"path":"a.txt","content":"old"}`); err != nil {
		t.Fatal(err)
	}
	off, err := mech.Parse("write_guard")
	if err != nil {
		t.Fatal(err)
	}
	env.Switches = off
	if _, err := call(t, env, "create_file", `{"path":"a.txt","content":"new"}`); err != nil {
		t.Fatalf("create_file with the guard off: %v", err)
	}
	if got := workspaceFile(t, env, "a.txt"); got != "new" {
		t.Fatalf("content = %q", got)
	}
}

func TestAppendCreatesAndExtends(t *testing.T) {
	env := newEnv(t)
	if _, err := call(t, env, "append_file", `{"path":"log/out.txt","content":"first "}`); err != nil {
		t.Fatalf("append to a new file: %v", err)
	}
	if _, err := call(t, env, "append_file", `{"path":"log/out.txt","content":"second"}`); err != nil {
		t.Fatalf("append: %v", err)
	}
	if got := workspaceFile(t, env, "log/out.txt"); got != "first second" {
		t.Fatalf("content = %q", got)
	}
}

func TestDeleteMovesToTrash(t *testing.T) {
	env := newEnv(t)
	if _, err := call(t, env, "create_file", `{"path":"gone.txt","content":"bye"}`); err != nil {
		t.Fatal(err)
	}
	out, err := call(t, env, "delete_file", `{"path":"gone.txt"}`)
	if err != nil || !strings.Contains(out, ".nanobird/trash/gone.txt@") {
		t.Fatalf("delete_file = %q, %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(env.Workspace.Root(), "gone.txt")); !os.IsNotExist(err) {
		t.Fatalf("the file is still there: %v", err)
	}
}

func TestListDirHidesStateDirectories(t *testing.T) {
	env := newEnv(t)
	root := env.Workspace.Root()
	for _, d := range []string{".git", ".nanobird", "src"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := call(t, env, "create_file", `{"path":"src/main.go","content":"package main\n"}`); err != nil {
		t.Fatal(err)
	}
	out, err := call(t, env, "list_dir", `{"depth":2}`)
	if err != nil {
		t.Fatalf("list_dir: %v", err)
	}
	if strings.Contains(out, ".git") || strings.Contains(out, ".nanobird") {
		t.Fatalf("state directories listed:\n%s", out)
	}
	if !strings.Contains(out, "src/\n") || !strings.Contains(out, "src/main.go\n") {
		t.Fatalf("list_dir = %q", out)
	}
}

func TestPathsOutsideTheWorkspaceAreRefused(t *testing.T) {
	env := newEnv(t)
	for _, c := range []struct{ name, args string }{
		{"read_file", `{"path":"../secret.txt"}`},
		{"create_file", `{"path":"/tmp/x.txt","content":"x"}`},
		{"list_dir", `{"path":"../.."}`},
	} {
		if _, err := call(t, env, c.name, c.args); err == nil {
			t.Errorf("%s %s was accepted", c.name, c.args)
		}
	}
}

func TestRunShellRunsInTheWorkspace(t *testing.T) {
	env := newEnv(t)
	out, err := call(t, env, "run_shell", `{"command":"echo nanobird"}`)
	if err != nil || !strings.Contains(out, "nanobird") || !strings.Contains(out, "[exit code 0]") {
		t.Fatalf("run_shell = %q, %v", out, err)
	}
}

func TestRunShellRefusesAndAsks(t *testing.T) {
	env := newEnv(t)
	if _, err := call(t, env, "run_shell", `{"command":"format C:"}`); err == nil || !strings.Contains(err.Error(), "ring 1") {
		t.Fatalf("format: %v", err)
	}
	if _, err := call(t, env, "run_shell", `{"command":"apt-get remove git"}`); err == nil || !strings.Contains(err.Error(), "permission") {
		t.Fatalf("apt-get remove: %v", err)
	}
}

func TestRunShellTimesOut(t *testing.T) {
	env := newEnv(t)
	sleep := "sleep 5"
	if strings.Contains(env.Shell.Name, "PowerShell") {
		sleep = "Start-Sleep -Seconds 5"
	}
	args, err := json.Marshal(map[string]any{"command": sleep, "timeout_s": 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, env, "run_shell", string(args)); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("slow command: %v", err)
	}
}

func TestTodoSetUpdateAndShow(t *testing.T) {
	env := newEnv(t)
	if _, err := call(t, env, "todo", `{"action":"set","items":["read the code","fix the bug","run the tests"]}`); err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, env, "todo", `{"action":"update","items":["1","fix the bug"]}`); err != nil {
		t.Fatalf("update: %v", err)
	}
	out, err := call(t, env, "todo", `{"action":"show"}`)
	if err != nil || !strings.Contains(out, "1. [x] read the code") || !strings.Contains(out, "3. [ ] run the tests") {
		t.Fatalf("show = %q, %v", out, err)
	}
	if got := env.Plan.Open(); len(got) != 1 || got[0] != "run the tests" {
		t.Fatalf("open items = %v", got)
	}
	if _, err := call(t, env, "todo", `{"action":"update","all":true}`); err != nil {
		t.Fatal(err)
	}
	if got := env.Plan.Open(); len(got) != 0 {
		t.Fatalf("open items after all=true: %v", got)
	}
	if _, err := call(t, env, "todo", `{"action":"update","items":["no such item"]}`); err == nil {
		t.Fatal("an unknown item was accepted")
	}
}

func TestCapKeepsHeadAndTail(t *testing.T) {
	s := strings.Repeat("a", 100) + strings.Repeat("b", 100)
	got := Cap(s, 40)
	if !strings.HasPrefix(got, strings.Repeat("a", 30)) || !strings.HasSuffix(got, strings.Repeat("b", 10)) {
		t.Fatalf("Cap = %q", got)
	}
	if !strings.Contains(got, "bytes left out") {
		t.Fatalf("Cap omitted the note: %q", got)
	}
	if Cap("short", 40) != "short" {
		t.Fatal("a short result was changed")
	}
	if !utf8.ValidString(Cap(strings.Repeat("é", 100), 41)) {
		t.Fatal("Cap split a character")
	}
}
