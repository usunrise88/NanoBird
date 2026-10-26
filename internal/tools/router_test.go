package tools

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/usunrise88/nanobird/internal/model"
)

func TestRouteByEnglishKeywords(t *testing.T) {
	cases := map[string][]string{
		"Create a file named a.txt":            {"files"},
		"Fix the failing test in the repo":     {"code"},
		"Remember this for later":              {"memory"},
		"Say hello":                            nil,
		"Rename the folder and delete the log": {"files"},
	}
	for task, want := range cases {
		if got := Route(task); !reflect.DeepEqual(got, want) {
			t.Errorf("Route(%q) = %v, want %v", task, got, want)
		}
	}
}

func hasTool(list []model.Tool, name string) bool {
	for _, t := range list {
		if t.Name == name {
			return true
		}
	}
	return false
}

func TestOfferedIsCorePlusEnabled(t *testing.T) {
	reg := NewRegistry(Builtins())
	core := reg.Offered(nil)
	for _, want := range []string{"read_file", "list_dir", "create_file", "edit_file", "run_shell", "todo", "skills", "tools", "finish"} {
		if !hasTool(core, want) {
			t.Errorf("core tool %s is not offered by default", want)
		}
	}
	if hasTool(core, "search_files") || hasTool(core, "append_file") {
		t.Fatal("routed tools were offered without being enabled")
	}
	if !hasTool(reg.Offered(map[string]bool{"code": true}), "search_files") {
		t.Fatal("enabling the code category did not offer its tools")
	}
	if !hasTool(reg.Offered(map[string]bool{"delete_file": true}), "delete_file") {
		t.Fatal("enabling a tool by name did not offer it")
	}
}

func TestToolsMetaEnablesACategory(t *testing.T) {
	env := newEnv(t)
	env.Catalog = NewRegistry(Builtins())
	out, err := call(t, env, "tools", `{"action":"enable","names":["code"]}`)
	if err != nil || !strings.Contains(out, "next turn") || !env.Enabled["code"] {
		t.Fatalf("enable = %q, %v (enabled %v)", out, err, env.Enabled)
	}
	if _, err := call(t, env, "tools", `{"action":"enable","names":["nothing"]}`); err == nil {
		t.Fatal("an unknown name was enabled")
	}
	found, err := call(t, env, "tools", `{"action":"find","query":"search"}`)
	if err != nil || !strings.Contains(found, "search_files") {
		t.Fatalf("find = %q, %v", found, err)
	}
	listed, err := call(t, env, "tools", `{"action":"list"}`)
	if err != nil || !strings.Contains(listed, "code (enabled)") {
		t.Fatalf("list = %q, %v", listed, err)
	}
}

func TestSkillsToolListsAndLoads(t *testing.T) {
	env := newEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lint.md"), []byte("---\ndescription: Run the linter.\n---\nRun make lint and fix what it reports.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	list, err := os.ReadDir(dir)
	if err != nil || len(list) != 1 {
		t.Fatal(err)
	}
	env.Skills = nil
	out, err := call(t, env, "skills", `{"action":"list"}`)
	if err != nil || out != "(no skills)" {
		t.Fatalf("list = %q, %v", out, err)
	}
	if _, err := call(t, env, "skills", `{"action":"load","name":"lint"}`); err == nil {
		t.Fatal("a missing skill was loaded")
	}
}

func TestSearchFilesFindsLinesAndSkipsState(t *testing.T) {
	env := newEnv(t)
	if _, err := call(t, env, "create_file", `{"path":"src/main.go","content":"package main\n// TODO: fix\n"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, env, "create_file", `{"path":"notes.md","content":"TODO later\n"}`); err != nil {
		t.Fatal(err)
	}
	out, err := call(t, env, "search_files", `{"pattern":"TODO","glob":"*.go"}`)
	if err != nil || out != "src/main.go:2: // TODO: fix\n" {
		t.Fatalf("search = %q, %v", out, err)
	}
	none, err := call(t, env, "search_files", `{"pattern":"absent"}`)
	if err != nil || none != "no matches" {
		t.Fatalf("no match = %q, %v", none, err)
	}
}

func TestMemoryStoresAndRecalls(t *testing.T) {
	env := newEnv(t)
	if out, err := call(t, env, "memory_recall", `{}`); err != nil || out != "no notes yet" {
		t.Fatalf("empty recall = %q, %v", out, err)
	}
	if _, err := call(t, env, "memory_store", `{"text":"the API uses\ntabs"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, env, "memory_store", `{"text":"deploy is on Fridays"}`); err != nil {
		t.Fatal(err)
	}
	out, err := call(t, env, "memory_recall", `{"query":"tabs"}`)
	if err != nil || out != "- the API uses tabs" {
		t.Fatalf("recall = %q, %v", out, err)
	}
}
