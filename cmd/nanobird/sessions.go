package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/usunrise88/nanobird/internal/safety"
	"github.com/usunrise88/nanobird/internal/session"
)

// sessionsCmd is `nanobird sessions list` and `nanobird sessions show ID`
// (SPEC §11.2). It reads the transcripts under the workspace.
func sessionsCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: nanobird sessions list | show ID [--cwd DIR]")
		return 2
	}
	verb := args[0]
	// A session id comes first, before the flags: the flag package stops at the
	// first word that is not a flag.
	var id string
	rest := args[1:]
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		id, rest = rest[0], rest[1:]
	}
	fs := flag.NewFlagSet("sessions "+verb, flag.ContinueOnError)
	fs.SetOutput(stderr)
	cwd := fs.String("cwd", ".", "workspace directory")
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	if id == "" && fs.NArg() == 1 {
		id = fs.Arg(0)
	}
	ws, err := safety.NewWorkspace(*cwd)
	if err != nil {
		fmt.Fprintf(stderr, "nanobird sessions: %v\n", err)
		return 1
	}
	switch verb {
	case "list":
		ids, err := session.List(ws.Root())
		if err != nil {
			fmt.Fprintf(stderr, "nanobird sessions: %v\n", err)
			return 1
		}
		for _, id := range ids {
			entries, err := session.Read(ws.Root(), id)
			if err != nil {
				fmt.Fprintf(stdout, "%s  unreadable: %v\n", id, err)
				continue
			}
			fmt.Fprintf(stdout, "%s  %s\n", id, stopOf(entries))
		}
		return 0
	case "show":
		if id == "" {
			fmt.Fprintln(stderr, "usage: nanobird sessions show ID [--cwd DIR]")
			return 2
		}
		entries, err := session.Read(ws.Root(), id)
		if err != nil {
			fmt.Fprintf(stderr, "nanobird sessions: %v\n", err)
			return 1
		}
		for _, e := range entries {
			fmt.Fprintf(stdout, "%4d %s %-12s %s\n", e.Seq, e.Time, e.Kind, describe(e))
		}
		return 0
	}
	fmt.Fprintf(stderr, "nanobird sessions: unknown verb %q; use list or show\n", verb)
	return 2
}

// stopOf names how a session ended, or says it did not.
func stopOf(entries []session.Entry) string {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Kind == "stop" {
			var s struct {
				Reason string `json:"reason"`
				Turns  int    `json:"turns"`
			}
			_ = json.Unmarshal(entries[i].Data, &s)
			return fmt.Sprintf("%s after %d turns", s.Reason, s.Turns)
		}
	}
	return "unfinished"
}

// describe renders one transcript record in a line.
func describe(e session.Entry) string {
	var d map[string]any
	_ = json.Unmarshal(e.Data, &d)
	str := func(k string) string { s, _ := d[k].(string); return s }
	firstLine := func(s string) string {
		line, _, _ := strings.Cut(s, "\n")
		if len(line) > 100 {
			line = line[:100] + "..."
		}
		return line
	}
	switch e.Kind {
	case "task":
		return firstLine(str("text"))
	case "request":
		msgs, _ := d["messages"].([]any)
		tools, _ := d["tools"].([]any)
		return fmt.Sprintf("turn %v, %d messages, %d tools", d["turn"], len(msgs), len(tools))
	case "tool_call":
		return fmt.Sprintf("%s %s", str("name"), firstLine(str("arguments")))
	case "tool_result":
		ok, _ := d["ok"].(bool)
		return fmt.Sprintf("%s ok=%t %s", str("name"), ok, firstLine(str("output")))
	case "reply":
		calls, _ := d["calls"].([]any)
		return fmt.Sprintf("finish=%s calls=%d text=%q", str("finish"), len(calls), firstLine(str("text")))
	case "intervention":
		return str("kind")
	case "mechanism":
		return fmt.Sprintf("%s %s", str("id"), str("event"))
	case "stop":
		return fmt.Sprintf("%s after %v turns", str("reason"), d["turns"])
	}
	return firstLine(string(e.Data))
}
