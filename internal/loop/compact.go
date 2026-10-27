package loop

import (
	"context"
	"fmt"
	"strings"

	"github.com/usunrise88/nanobird/internal/mech"
	"github.com/usunrise88/nanobird/internal/model"
	"github.com/usunrise88/nanobird/internal/tools"
)

// Staged compaction of the context (SPEC §4.4, N3).
const (
	// defaultContextTokens is the context assumed when the configuration sets none.
	defaultContextTokens = 32768
	// defaultCompactionAt is the share of the context that starts compaction.
	defaultCompactionAt = 0.9
	// keepTail is how many of the latest messages compaction never touches.
	keepTail = 8
	// digestMin is the output size from which level 1 writes a digest.
	digestMin = 200
	// summaryInput caps the transcript that the summary request reads.
	summaryInput = 12288
	// summaryPrompt is the system prompt of the summary request. It is English (SPEC §5.13).
	summaryPrompt = "You summarise the earlier steps of a coding task for the agent that continues it. Write in English, at most 300 words. Keep the file names, the decisions, the results of tests and what is still open. Do not call tools."
)

// compactIfNeeded shortens the context once the prompt nears its budget (SPEC
// §4.4). It works from the prompt size the engine reported for the last request,
// so the first turns run unchanged. Level 1 digests old tool outputs. Level 3
// replaces the oldest turns with a summary. The system prompt, the task and the
// plan are never compacted (M3d). Level 2, old reasoning, has nothing to drop:
// NanoBird does not keep reasoning in the history.
func (l *Loop) compactIfNeeded(ctx context.Context, turn int, st *state) error {
	if !l.on(mech.Compaction) {
		return nil
	}
	// After a reset, or a restore from a checkpoint, the marker can point past the
	// messages it counted, so it starts again from nothing.
	if st.sentLen > len(st.msgs) {
		st.lastPrompt, st.sentLen = 0, 0
	}
	if st.lastPrompt == 0 {
		return nil
	}
	limit := int(float64(l.contextTokens()) * l.compactionAt())
	if st.lastPrompt+mech.TokensOf(bytesOf(st.msgs[st.sentLen:])) < limit {
		return nil
	}
	cut := tailCut(st.msgs)
	if cut <= 2 {
		return l.fitWithoutTail(st, turn)
	}
	if n := dropOutputs(st.msgs, cut); n > 0 {
		l.write("mechanism", mechanismRecord{ID: mech.Compaction, Turn: turn, Event: "compact", Level: 1, Count: n})
	}
	// The tool schemas are part of every prompt, so they count toward the budget too.
	if mech.TokensOf(bytesOf(st.msgs)+schemaBytes(l.tools.Offered(l.env.Enabled))) < limit {
		st.lastPrompt, st.sentLen = 0, 0
		return nil
	}
	summary, err := l.summarize(ctx, turn, st.msgs[2:cut])
	if err != nil {
		return err
	}
	st.msgs = l.rebuild(st.msgs, cut, summary)
	st.lastPrompt, st.sentLen = 0, 0
	l.write("mechanism", mechanismRecord{ID: mech.Compaction, Turn: turn, Event: "compact", Level: 3, Count: cut - 2})
	return nil
}

// tailCut returns the index where the kept tail starts: keepTail messages from
// the end, moved back so a tool result never starts the tail away from its call.
func tailCut(msgs []model.Message) int {
	cut := len(msgs) - keepTail
	for cut > 2 && msgs[cut].Role == model.RoleTool {
		cut--
	}
	return cut
}

// dropOutputs replaces the long outputs of the tool calls before cut with
// digests (level 1). It returns how many it replaced.
func dropOutputs(msgs []model.Message, cut int) int {
	names := map[string]string{}
	for _, m := range msgs[:cut] {
		for _, c := range m.ToolCalls {
			names[c.ID] = c.Name
		}
	}
	dropped := 0
	for i := 2; i < cut; i++ {
		m := &msgs[i]
		if m.Role != model.RoleTool || len(m.Content) <= digestMin {
			continue
		}
		m.Content = mech.Digest(m.ToolCallID, names[m.ToolCallID], m.Content)
		dropped++
	}
	return dropped
}

// summarize asks the model for a summary of the turns that compaction drops
// (level 3). The request carries no tools.
func (l *Loop) summarize(ctx context.Context, turn int, old []model.Message) (string, error) {
	msgs := []model.Message{
		{Role: model.RoleSystem, Content: summaryPrompt},
		{Role: model.RoleUser, Content: tools.Cap(transcriptOf(old), summaryInput)},
	}
	r, err := l.generate(ctx, turn, msgs, nil)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(r.Text), nil
}

// rebuild puts the summary in place of the turns before cut. The system prompt,
// the task and the plan stay, and the plan comes back verbatim (M3d).
func (l *Loop) rebuild(msgs []model.Message, cut int, summary string) []model.Message {
	out := []model.Message{msgs[0], msgs[1]}
	if len(l.env.Plan.Items) > 0 {
		out = append(out, model.Message{Role: model.RoleUser, Content: "The plan, kept verbatim:\n" + l.env.Plan.Render()})
	}
	out = append(out, model.Message{Role: model.RoleUser, Content: "Summary of the earlier steps, written by the harness:\n" + summary})
	return append(out, msgs[cut:]...)
}

// transcriptOf writes messages as plain lines for the summary request.
func transcriptOf(msgs []model.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		fmt.Fprintf(&b, "%s: %s\n", m.Role, m.Content)
		for _, c := range m.ToolCalls {
			fmt.Fprintf(&b, "  tool call %s %s\n", c.Name, c.Arguments)
		}
	}
	return b.String()
}

// schemaBytes counts the text of the tool schemas a request carries.
func schemaBytes(list []model.Tool) int {
	n := 0
	for _, t := range list {
		n += len(t.Name) + len(t.Description) + len(t.Parameters)
	}
	return n
}

// bytesOf counts the text a list of messages carries.
func bytesOf(msgs []model.Message) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Content)
		for _, c := range m.ToolCalls {
			n += len(c.Name) + len(c.Arguments)
		}
	}
	return n
}

func (l *Loop) contextTokens() int {
	if l.cfg.ContextTokens > 0 {
		return l.cfg.ContextTokens
	}
	return defaultContextTokens
}

func (l *Loop) compactionAt() float64 {
	if l.cfg.CompactionAt > 0 {
		return l.cfg.CompactionAt
	}
	return defaultCompactionAt
}

// fitWithoutTail handles a prompt that is too big while the tail holds too few
// messages to compact. It digests every tool result but the last, and when the
// messages still do not fit the window it reports an overflow, instead of sending a
// prompt that the engine refuses (SPEC §4.4).
func (l *Loop) fitWithoutTail(st *state, turn int) error {
	if n := dropOutputs(st.msgs, len(st.msgs)-1); n > 0 {
		l.write("mechanism", mechanismRecord{ID: mech.Compaction, Turn: turn, Event: "compact", Level: 1, Count: n})
	}
	size := mech.TokensOf(bytesOf(st.msgs) + schemaBytes(l.tools.Offered(l.env.Enabled)))
	if size >= l.contextTokens() {
		return fmt.Errorf("%w: the messages come to about %d tokens and the window holds %d", model.ErrContextFull, size, l.contextTokens())
	}
	st.lastPrompt, st.sentLen = 0, 0
	return nil
}
