package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/usunrise88/nanobird/internal/mech"
	"github.com/usunrise88/nanobird/internal/model"
	"github.com/usunrise88/nanobird/internal/tools"
)

// checkpointFile is the checkpoint in a session directory (SPEC §9.3).
const checkpointFile = "checkpoint.json"

// checkpointVersion is the layout of the checkpoint file.
const checkpointVersion = 1

// checkpoint is the state of a run after a completed turn. Resume restarts at
// the turn after it, with the same messages, plan and counters. Records written
// after the checkpoint stay in the transcript, and the resumed run repeats the
// work they describe; the write guard and the edit checks catch a repeated write.
type checkpoint struct {
	Version       int                 `json:"version"`
	Task          string              `json:"task"`
	Turn          int                 `json:"turn"`
	Seq           int                 `json:"seq"`
	Messages      []savedMessage      `json:"messages"`
	Plan          []tools.PlanItem    `json:"plan"`
	Enabled       map[string]bool     `json:"enabled"`
	Productive    int                 `json:"productive"`
	LastShell     string              `json:"last_shell"`
	LastPrompt    int                 `json:"last_prompt"`
	SentLen       int                 `json:"sent_len"`
	LastRejection string              `json:"last_rejection"`
	Ladder        mech.LadderState    `json:"ladder"`
	Anti          mech.LoopState      `json:"anti"`
	Gate          mech.GateState      `json:"gate"`
	Convergence   mech.ConvState      `json:"convergence"`
	Malformed     mech.MalformedState `json:"malformed"`
}

// savedMessage is a message of the history, with the names the file uses.
type savedMessage struct {
	Role       string      `json:"role"`
	Content    string      `json:"content"`
	ToolCalls  []savedCall `json:"tool_calls,omitempty"`
	ToolCallID string      `json:"tool_call_id,omitempty"`
}

type savedCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// checkpoint writes the state after turn to the session directory. The file is
// replaced in one step, so a kill during the write keeps the previous checkpoint.
func (l *Loop) checkpoint(st *state, turn int) {
	if l.err != nil {
		return
	}
	cp := checkpoint{
		Version:       checkpointVersion,
		Task:          st.task,
		Turn:          turn,
		Seq:           l.log.Seq(),
		Messages:      savedMessages(st.msgs),
		Plan:          l.env.Plan.Items,
		Enabled:       l.env.Enabled,
		Productive:    st.productive,
		LastShell:     st.lastShell,
		LastPrompt:    st.lastPrompt,
		SentLen:       st.sentLen,
		LastRejection: st.lastRejection,
		Ladder:        st.ladder.State(),
		Anti:          st.anti.State(),
		Gate:          st.gate.State(),
		Convergence:   st.conv.State(),
		Malformed:     st.malformed.State(),
	}
	data, err := json.MarshalIndent(cp, "", "  ")
	if err == nil {
		err = writeFileAtomic(filepath.Join(l.log.Dir(), checkpointFile), data)
	}
	if err != nil {
		l.err = fmt.Errorf("checkpoint: %w", err)
	}
}

// Resume continues the session of the log from its last checkpoint (SPEC §9.3,
// M7). The run goes on at the turn after the checkpoint.
func (l *Loop) Resume(ctx context.Context) Result {
	cp, err := readCheckpoint(l.log.Dir())
	if err != nil {
		return l.stop(Result{Stop: StopError, Err: err})
	}
	st := &state{
		task:          cp.Task,
		msgs:          messagesOf(cp.Messages),
		anti:          mech.RestoreLoopTracker(cp.Anti),
		ladder:        mech.RestoreLadder(cp.Ladder),
		gate:          mech.RestoreGate(cp.Gate),
		conv:          mech.RestoreConvergence(cp.Convergence),
		malformed:     mech.RestoreMalformed(cp.Malformed),
		productive:    cp.Productive,
		lastShell:     cp.LastShell,
		lastRejection: cp.LastRejection,
		lastPrompt:    cp.LastPrompt,
		sentLen:       cp.SentLen,
	}
	l.env.Plan.Items = cp.Plan
	l.env.Enabled = cp.Enabled
	if l.env.Enabled == nil {
		l.env.Enabled = map[string]bool{}
	}
	l.write("resume", resumeRecord{FromTurn: cp.Turn})
	res := l.run(ctx, st, cp.Turn+1)
	l.history = st.msgs
	return res
}

// readCheckpoint loads the checkpoint of a session directory.
func readCheckpoint(dir string) (checkpoint, error) {
	var cp checkpoint
	data, err := os.ReadFile(filepath.Join(dir, checkpointFile))
	if errors.Is(err, os.ErrNotExist) {
		return cp, errors.New("no checkpoint in this session; nothing to resume")
	}
	if err != nil {
		return cp, err
	}
	if err := json.Unmarshal(data, &cp); err != nil {
		return cp, fmt.Errorf("checkpoint: %w", err)
	}
	if cp.Version != checkpointVersion {
		return cp, fmt.Errorf("checkpoint version %d is not supported", cp.Version)
	}
	return cp, nil
}

// writeFileAtomic writes data to a temporary file and renames it over path.
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func savedMessages(msgs []model.Message) []savedMessage {
	out := make([]savedMessage, 0, len(msgs))
	for _, m := range msgs {
		sm := savedMessage{Role: string(m.Role), Content: m.Content, ToolCallID: m.ToolCallID}
		for _, c := range m.ToolCalls {
			sm.ToolCalls = append(sm.ToolCalls, savedCall{ID: c.ID, Name: c.Name, Arguments: c.Arguments})
		}
		out = append(out, sm)
	}
	return out
}

func messagesOf(saved []savedMessage) []model.Message {
	out := make([]model.Message, 0, len(saved))
	for _, sm := range saved {
		m := model.Message{Role: model.Role(sm.Role), Content: sm.Content, ToolCallID: sm.ToolCallID}
		for _, c := range sm.ToolCalls {
			m.ToolCalls = append(m.ToolCalls, model.ToolCall{ID: c.ID, Name: c.Name, Arguments: c.Arguments})
		}
		out = append(out, m)
	}
	return out
}
