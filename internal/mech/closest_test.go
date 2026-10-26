package mech

import "testing"

func TestClosestPutsTheNearestNameFirst(t *testing.T) {
	known := []Suggestion{
		{Name: "finish", Description: "End the task with a summary. The harness checks it."},
		{Name: "read_file", Description: "Read a text file in the workspace. Lines are numbered."},
		{Name: "list_dir", Description: "List the entries of a directory."},
		{Name: "edit_file", Description: "Replace exact text in a file."},
	}
	got := Closest("read_fle", known)
	if len(got) != 3 || got[0].Name != "read_file" {
		t.Fatalf("Closest = %+v", got)
	}
	if got[0].Description != "Read a text file in the workspace." {
		t.Fatalf("description = %q", got[0].Description)
	}
}
