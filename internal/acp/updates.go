package acp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	sdk "github.com/coder/acp-go-sdk"
	"github.com/usunrise88/nanobird/internal/model"
	"github.com/usunrise88/nanobird/internal/session"
	"github.com/usunrise88/nanobird/internal/tools"
)

// updates sends what a session's runs do, as session updates, in the order that
// they happen (SPEC §8.3). It also keeps the text streamed in the current prompt,
// so the prompt can tell whether its summary was said already.
type updates struct {
	agent    *Agent
	id       sdk.SessionId
	root     string
	streamed strings.Builder
	// call is the id of the tool call that runs, which a permission request names.
	call string
}

// Reasoning sends a piece of the model's thinking as it streams (SPEC §8.3).
func (u *updates) Reasoning(piece string) {
	u.send(sdk.UpdateAgentThoughtText(piece))
}

// Usage reports the tokens of the last reply and the size of the context they count
// against (SPEC §8.3).
func (u *updates) Usage(used, size int) {
	u.send(sdk.SessionUpdate{UsageUpdate: &sdk.SessionUsageUpdate{Used: used, Size: size, SessionUpdate: "usage_update"}})
}

// title sends the title of the session, which is its first task (SPEC §8.3).
func (u *updates) title(task string) {
	t := titleText(task)
	u.send(sdk.SessionUpdate{SessionInfoUpdate: &sdk.SessionSessionInfoUpdate{Title: &t, SessionUpdate: "session_info_update"}})
}

// titleText is the title of a session: the first line of its first task, cut to 80
// characters.
func titleText(task string) string {
	line, _, _ := strings.Cut(task, "\n")
	line = strings.TrimSpace(line)
	if r := []rune(line); len(r) > 80 {
		line = string(r[:80])
	}
	return line
}

// send sends one update. A notification has no answer, so a failure to send does
// not stop the run.
func (u *updates) send(update sdk.SessionUpdate) {
	<-u.agent.ready
	_ = u.agent.conn.SessionUpdate(context.Background(), sdk.SessionNotification{SessionId: u.id, Update: update})
}

// Text sends a piece of the model's reply as it streams.
func (u *updates) Text(piece string) {
	u.streamed.WriteString(piece)
	u.send(sdk.UpdateAgentMessageText(piece))
}

// ToolCall reports a call as in progress. The loop runs it, unless a guard refuses
// it, and then ToolDone says so.
func (u *updates) ToolCall(call model.ToolCall) {
	u.call = call.ID
	a := argsOf(call)
	opts := []sdk.ToolCallStartOpt{
		sdk.WithStartKind(kindOf(call.Name)),
		sdk.WithStartStatus(sdk.ToolCallStatusInProgress),
	}
	if json.Valid([]byte(call.Arguments)) {
		opts = append(opts, sdk.WithStartRawInput(json.RawMessage(call.Arguments)))
	}
	if a.Path != "" {
		opts = append(opts, sdk.WithStartLocations([]sdk.ToolCallLocation{{Path: u.abs(a.Path)}}))
	}
	u.send(sdk.StartToolCall(sdk.ToolCallId(call.ID), call.Name, opts...))
}

// ToolDone reports how a call ended. An edit shows its diff, and other output goes
// to the client whole. A refused or failed call is failed, with its message.
func (u *updates) ToolDone(call model.ToolCall, ok bool, output string) {
	status := sdk.ToolCallStatusCompleted
	content := []sdk.ToolCallContent{sdk.ToolContent(sdk.TextBlock(output))}
	if !ok {
		status = sdk.ToolCallStatusFailed
	} else if diff, found := u.diffOf(call); found {
		content = []sdk.ToolCallContent{diff}
	}
	u.send(sdk.UpdateToolCall(sdk.ToolCallId(call.ID),
		sdk.WithUpdateStatus(status),
		sdk.WithUpdateContent(content),
		sdk.WithUpdateRawOutput(output),
	))
}

// Plan mirrors the todo list: a done item is completed, and the others are pending.
func (u *updates) Plan(items []tools.PlanItem) {
	entries := make([]sdk.PlanEntry, 0, len(items))
	for _, it := range items {
		status := sdk.PlanEntryStatusPending
		if it.Done {
			status = sdk.PlanEntryStatusCompleted
		}
		entries = append(entries, sdk.PlanEntry{Content: it.Text, Priority: sdk.PlanEntryPriorityMedium, Status: status})
	}
	u.send(sdk.UpdatePlan(entries...))
}

// diffOf is the diff that an edit or a new file shows, when there is one.
func (u *updates) diffOf(call model.ToolCall) (sdk.ToolCallContent, bool) {
	a := argsOf(call)
	switch call.Name {
	case "edit_file":
		return sdk.ToolDiffContent(u.abs(a.Path), a.New, a.Old), true
	case "create_file":
		return sdk.ToolDiffContent(u.abs(a.Path), a.Content), true
	}
	return sdk.ToolCallContent{}, false
}

// abs is the absolute path of a workspace path, since the protocol wants absolute
// paths (SPEC §8).
func (u *updates) abs(rel string) string {
	if filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Join(u.root, filepath.FromSlash(rel))
}

// callArgs are the arguments that the updates show.
type callArgs struct {
	Path    string `json:"path"`
	Old     string `json:"old"`
	New     string `json:"new"`
	Content string `json:"content"`
}

// argsOf reads the arguments of a call. Arguments that do not parse show nothing.
func argsOf(call model.ToolCall) callArgs {
	var a callArgs
	_ = json.Unmarshal([]byte(call.Arguments), &a)
	return a
}

// kinds maps a tool to the kind the protocol shows for it (SPEC §8.3). A tool that
// is not listed is other.
var kinds = map[string]sdk.ToolKind{
	"read_file":    sdk.ToolKindRead,
	"list_dir":     sdk.ToolKindRead,
	"search_files": sdk.ToolKindSearch,
	"create_file":  sdk.ToolKindEdit,
	"edit_file":    sdk.ToolKindEdit,
	"append_file":  sdk.ToolKindEdit,
	"rollback":     sdk.ToolKindEdit,
	"delete_file":  sdk.ToolKindDelete,
	"run_shell":    sdk.ToolKindExecute,
	"run_tests":    sdk.ToolKindExecute,
	"web_fetch":    sdk.ToolKindFetch,
	"web_search":   sdk.ToolKindFetch,
	"todo":         sdk.ToolKindThink,
}

// kindOf is the protocol kind of a tool.
func kindOf(name string) sdk.ToolKind {
	if k, ok := kinds[name]; ok {
		return k
	}
	return sdk.ToolKindOther
}

// replay sends the history of a session, from its transcript: the prompts and the
// replies, the calls with their results, and the end of each prompt, in the order
// that they happened (SPEC §8.2).
func (u *updates) replay(entries []session.Entry) {
	calls := map[string]model.ToolCall{}
	var said strings.Builder
	for _, e := range entries {
		switch e.Kind {
		case "task":
			var d struct {
				Text string `json:"text"`
			}
			if json.Unmarshal(e.Data, &d) == nil {
				u.send(sdk.UpdateUserMessageText(d.Text))
			}
			said.Reset()
		case "reply":
			var d struct {
				Text string `json:"text"`
			}
			if json.Unmarshal(e.Data, &d) == nil && d.Text != "" {
				said.WriteString(d.Text)
				u.send(sdk.UpdateAgentMessageText(d.Text))
			}
		case "tool_call":
			var d struct {
				ID        string `json:"id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}
			if json.Unmarshal(e.Data, &d) == nil {
				call := model.ToolCall{ID: d.ID, Name: d.Name, Arguments: d.Arguments}
				calls[d.ID] = call
				u.ToolCall(call)
			}
		case "tool_result":
			var d struct {
				ID     string `json:"id"`
				OK     bool   `json:"ok"`
				Output string `json:"output"`
			}
			if json.Unmarshal(e.Data, &d) == nil {
				if call, ok := calls[d.ID]; ok {
					u.ToolDone(call, d.OK, d.Output)
				}
			}
		case "stop":
			var d struct {
				Summary    string `json:"summary"`
				Unverified bool   `json:"unverified"`
				Blocker    string `json:"blocker"`
			}
			if json.Unmarshal(e.Data, &d) == nil {
				u.ending(d.Summary, d.Blocker, d.Unverified, said.String())
			}
		}
	}
}

// ending says how a prompt ended, in words the client shows: the summary, when the
// text said so far does not already hold it, a blocker, and an unverified finish
// (SPEC §4.3, §8.3).
func (u *updates) ending(summary, blocker string, unverified bool, said string) {
	if summary != "" && !strings.Contains(said, summary) {
		u.send(sdk.UpdateAgentMessageText(summary))
	}
	if blocker != "" {
		u.send(sdk.UpdateAgentMessageText("Blocked: " + blocker))
	}
	if unverified {
		u.send(sdk.UpdateAgentMessageText("The finish was accepted after repeated rejections, so it is unverified."))
	}
}

// titleOf is the title of a session: its first task, or nothing when it has none.
func titleOf(entries []session.Entry) *string {
	for _, e := range entries {
		if e.Kind != "task" {
			continue
		}
		var d struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(e.Data, &d) == nil && d.Text != "" {
			return &d.Text
		}
		return nil
	}
	return nil
}

// Mode reports the decision for a prompt, chat or task (SPEC §4.1, §8.3).
func (u *updates) Mode(mode string) {
	u.send(sdk.SessionUpdate{CurrentModeUpdate: &sdk.SessionCurrentModeUpdate{CurrentModeId: sdk.SessionModeId(mode), SessionUpdate: "current_mode_update"}})
}

// Permit asks the client whether a call that a safety ring stopped may run (SPEC
// §8.5). The options are allow_once and reject_once, and allow_always when the ring
// offers it. A refusal, or a cancelled prompt, is a denial.
func (u *updates) Permit(ctx context.Context, p tools.Permission) tools.Answer {
	opts := []sdk.PermissionOption{
		{OptionId: "allow_once", Kind: sdk.PermissionOptionKindAllowOnce, Name: "Allow once"},
		{OptionId: "reject_once", Kind: sdk.PermissionOptionKindRejectOnce, Name: "Reject"},
	}
	if p.Family != "" {
		opts = append(opts, sdk.PermissionOption{OptionId: "allow_always", Kind: sdk.PermissionOptionKindAllowAlways, Name: "Always allow " + p.Family})
	}
	<-u.agent.ready
	title := p.Reason
	call := sdk.ToolCallUpdate{ToolCallId: sdk.ToolCallId(u.call), Title: &title}
	if p.Command != "" {
		call.RawInput = map[string]string{"command": p.Command}
	}
	resp, err := u.agent.conn.RequestPermission(ctx, sdk.RequestPermissionRequest{SessionId: u.id, ToolCall: call, Options: opts})
	if err != nil || resp.Outcome.Selected == nil {
		return tools.Deny
	}
	switch resp.Outcome.Selected.OptionId {
	case "allow_once":
		return tools.AllowOnce
	case "allow_always":
		if p.Family != "" {
			return tools.AllowAlways
		}
	}
	return tools.Deny
}
