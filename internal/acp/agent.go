// Package acp is nanobird as an ACP agent: sessions, prompts and session updates
// over stdio (SPEC §8), on coder/acp-go-sdk. Nothing below it imports it (SPEC §3).
package acp

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	sdk "github.com/coder/acp-go-sdk"
	"github.com/usunrise88/nanobird/internal/loop"
	"github.com/usunrise88/nanobird/internal/session"
)

// Runner runs the prompts of one session in one workspace (SPEC §8.2). The command
// builds it from the workspace's configuration, so this package reads none.
type Runner interface {
	// ID is the session's id, which names its transcript.
	ID() string
	// Prompt runs one task to its stop. The observer that the runner was opened
	// with hears the run.
	Prompt(ctx context.Context, task string) loop.Result
	// Close releases what the session holds.
	Close()
}

// Open starts a session in a workspace. An id that is not empty names a session of
// that workspace to continue, from its transcript and its last checkpoint. The
// observer hears what the session's prompts do.
type Open func(workspace, id string, obs loop.Observer) (Runner, error)

// Agent serves ACP sessions. Each session has a Runner, and a prompt runs on it.
type Agent struct {
	open    Open
	version string
	// ready is closed once Serve has the connection, so that no update is sent
	// before there is somewhere to send it.
	ready chan struct{}
	conn  *sdk.AgentSideConnection

	mu       sync.Mutex
	sessions map[sdk.SessionId]*activeSession
}

// NewAgent returns an agent that opens its sessions with open. Serve connects it
// to a client.
func NewAgent(open Open, version string) *Agent {
	return &Agent{open: open, version: version, ready: make(chan struct{}), sessions: map[sdk.SessionId]*activeSession{}}
}

// activeSession is one open session. cancel stops the prompt that runs, if one
// does. closing is set when the session is closed, and then the runner is closed
// once no prompt runs.
type activeSession struct {
	id      sdk.SessionId
	runner  Runner
	obs     *updates
	mu      sync.Mutex
	cancel  context.CancelFunc
	closing bool
}

// Initialize answers the handshake: ACP v1, no authentication, text prompts, and the
// session methods that this agent serves.
func (a *Agent) Initialize(context.Context, sdk.InitializeRequest) (sdk.InitializeResponse, error) {
	return sdk.InitializeResponse{
		ProtocolVersion: sdk.ProtocolVersionNumber,
		AgentInfo:       &sdk.Implementation{Name: "nanobird", Version: a.version},
		AgentCapabilities: sdk.AgentCapabilities{
			LoadSession: true,
			SessionCapabilities: sdk.SessionCapabilities{
				Close:  &sdk.SessionCloseCapabilities{},
				List:   &sdk.SessionListCapabilities{},
				Resume: &sdk.SessionResumeCapabilities{},
			},
		},
		AuthMethods: []sdk.AuthMethod{},
	}, nil
}

// Authenticate has no method to run: the model key lives in the NanoBird config
// (SPEC §8.2).
func (a *Agent) Authenticate(context.Context, sdk.AuthenticateRequest) (sdk.AuthenticateResponse, error) {
	return sdk.AuthenticateResponse{}, sdk.NewMethodNotFound("authenticate")
}

// NewSession opens a session in the workspace at cwd, which must be an absolute
// path (SPEC §8.2). The session's id names its transcript.
func (a *Agent) NewSession(_ context.Context, p sdk.NewSessionRequest) (sdk.NewSessionResponse, error) {
	if !filepath.IsAbs(p.Cwd) {
		return sdk.NewSessionResponse{}, sdk.NewInvalidParams("cwd must be an absolute path")
	}
	s, err := a.start(p.Cwd, "")
	if err != nil {
		return sdk.NewSessionResponse{}, err
	}
	return sdk.NewSessionResponse{SessionId: s.id}, nil
}

// LoadSession continues a session of the workspace, and replays its history to the
// client as session updates before it answers (SPEC §8.2).
func (a *Agent) LoadSession(_ context.Context, p sdk.LoadSessionRequest) (sdk.LoadSessionResponse, error) {
	s, err := a.attach(p.Cwd, p.SessionId)
	if err != nil {
		return sdk.LoadSessionResponse{}, err
	}
	entries, err := session.Read(p.Cwd, string(p.SessionId))
	if err != nil {
		return sdk.LoadSessionResponse{}, sdk.NewInternalError(err.Error())
	}
	s.obs.replay(entries)
	return sdk.LoadSessionResponse{}, nil
}

// ResumeSession continues a session of the workspace without replaying its history
// (SPEC §8.2).
func (a *Agent) ResumeSession(_ context.Context, p sdk.ResumeSessionRequest) (sdk.ResumeSessionResponse, error) {
	if _, err := a.attach(p.Cwd, p.SessionId); err != nil {
		return sdk.ResumeSessionResponse{}, err
	}
	return sdk.ResumeSessionResponse{}, nil
}

// ListSessions lists the sessions of the workspace at cwd, each with its first task
// as the title. Without a cwd it lists the sessions that are open (SPEC §8.2).
func (a *Agent) ListSessions(_ context.Context, p sdk.ListSessionsRequest) (sdk.ListSessionsResponse, error) {
	if p.Cwd == nil {
		return sdk.ListSessionsResponse{Sessions: a.openSessions()}, nil
	}
	ids, err := session.List(*p.Cwd)
	if errors.Is(err, fs.ErrNotExist) {
		ids = nil
	} else if err != nil {
		return sdk.ListSessionsResponse{}, sdk.NewInternalError(err.Error())
	}
	infos := []sdk.SessionInfo{}
	for _, id := range ids {
		entries, err := session.Read(*p.Cwd, id)
		if err != nil {
			return sdk.ListSessionsResponse{}, sdk.NewInternalError(err.Error())
		}
		info := sdk.SessionInfo{SessionId: sdk.SessionId(id), Cwd: *p.Cwd, Title: titleOf(entries)}
		if len(entries) > 0 {
			info.UpdatedAt = &entries[len(entries)-1].Time
		}
		infos = append(infos, info)
	}
	return sdk.ListSessionsResponse{Sessions: infos}, nil
}

// CloseSession ends a session. A prompt that runs is cancelled, and the session's
// resources are released. The transcript stays on disk (SPEC §8.2).
func (a *Agent) CloseSession(_ context.Context, p sdk.CloseSessionRequest) (sdk.CloseSessionResponse, error) {
	a.mu.Lock()
	s, ok := a.sessions[p.SessionId]
	delete(a.sessions, p.SessionId)
	a.mu.Unlock()
	if !ok {
		return sdk.CloseSessionResponse{}, sdk.NewInvalidParams("no such session: " + string(p.SessionId))
	}
	s.close()
	return sdk.CloseSessionResponse{}, nil
}

// Prompt runs one task on the session and answers with its stop reason (SPEC
// §4.3, §8.2). A prompt that is cancelled answers cancelled, not an error.
func (a *Agent) Prompt(ctx context.Context, p sdk.PromptRequest) (sdk.PromptResponse, error) {
	s, err := a.session(p.SessionId)
	if err != nil {
		return sdk.PromptResponse{}, err
	}
	task, err := taskOf(p.Prompt)
	if err != nil {
		return sdk.PromptResponse{}, err
	}
	run, cancel := context.WithCancel(ctx)
	defer cancel()
	if !s.begin(cancel) {
		return sdk.PromptResponse{}, sdk.NewInvalidRequest("the session cannot take a prompt now: one is running, or it is closing")
	}
	defer s.end()
	s.obs.streamed.Reset()
	res := s.runner.Prompt(run, task)
	return a.answer(s, res)
}

// Cancel stops the prompt that runs in the session. The prompt answers cancelled.
func (a *Agent) Cancel(_ context.Context, p sdk.CancelNotification) error {
	s, err := a.session(p.SessionId)
	if err != nil {
		return err
	}
	s.stop()
	return nil
}

// The methods that are not built yet answer method not found, so that a client can
// tell them from a failure (SPEC §8.2).

// Logout has nothing to end.
func (a *Agent) Logout(context.Context, sdk.LogoutRequest) (sdk.LogoutResponse, error) {
	return sdk.LogoutResponse{}, sdk.NewMethodNotFound("logout")
}

// SetSessionConfigOption is not built yet.
func (a *Agent) SetSessionConfigOption(context.Context, sdk.SetSessionConfigOptionRequest) (sdk.SetSessionConfigOptionResponse, error) {
	return sdk.SetSessionConfigOptionResponse{}, sdk.NewMethodNotFound("session/set_config_option")
}

// SetSessionMode is not built yet.
func (a *Agent) SetSessionMode(context.Context, sdk.SetSessionModeRequest) (sdk.SetSessionModeResponse, error) {
	return sdk.SetSessionModeResponse{}, sdk.NewMethodNotFound("session/set_mode")
}

// start opens a session in the workspace at cwd, continuing the session id when it
// is not empty, and registers it under its id.
func (a *Agent) start(cwd, id string) (*activeSession, error) {
	obs := &updates{agent: a, root: cwd}
	runner, err := a.open(cwd, id, obs)
	if err != nil {
		return nil, sdk.NewInternalError(err.Error())
	}
	s := &activeSession{id: sdk.SessionId(runner.ID()), runner: runner, obs: obs}
	obs.id = s.id
	a.mu.Lock()
	a.sessions[s.id] = s
	a.mu.Unlock()
	return s, nil
}

// attach returns the session with id, opening it from its transcript when it is not
// open yet.
func (a *Agent) attach(cwd string, id sdk.SessionId) (*activeSession, error) {
	if !filepath.IsAbs(cwd) {
		return nil, sdk.NewInvalidParams("cwd must be an absolute path")
	}
	a.mu.Lock()
	s, ok := a.sessions[id]
	a.mu.Unlock()
	if ok {
		return s, nil
	}
	return a.start(cwd, string(id))
}

// session looks up an open session by id.
func (a *Agent) session(id sdk.SessionId) (*activeSession, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[id]
	if !ok {
		return nil, sdk.NewInvalidParams("no such session: " + string(id))
	}
	return s, nil
}

// openSessions lists the open sessions, by id.
func (a *Agent) openSessions() []sdk.SessionInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := []sdk.SessionInfo{}
	for id, s := range a.sessions {
		out = append(out, sdk.SessionInfo{SessionId: id, Cwd: s.obs.root})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SessionId < out[j].SessionId })
	return out
}

// begin records the cancel of a prompt that starts. It reports false when a prompt
// already runs, or the session is closing: a session runs one prompt at a time.
func (s *activeSession) begin(cancel context.CancelFunc) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil || s.closing {
		return false
	}
	s.cancel = cancel
	return true
}

// end forgets the prompt that finished, and closes the runner if the session was
// closed while the prompt ran.
func (s *activeSession) end() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancel = nil
	if s.closing {
		s.runner.Close()
	}
}

// stop cancels the prompt that runs, if any.
func (s *activeSession) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
}

// close ends the session. A prompt that runs is cancelled first, and it closes the
// runner when it ends. Without a prompt, the runner closes now.
func (s *activeSession) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closing = true
	if s.cancel != nil {
		s.cancel()
		return
	}
	s.runner.Close()
}

// answer turns the end of a run into the answer to session/prompt. A run that
// ended with a summary the client has not seen gets it as a last message, and a
// blocker or an unverified finish is said in words (SPEC §4.3, §8.3).
func (a *Agent) answer(s *activeSession, res loop.Result) (sdk.PromptResponse, error) {
	if res.Stop == loop.StopError {
		msg := "the run failed"
		if res.Err != nil {
			msg = res.Err.Error()
		}
		return sdk.PromptResponse{}, sdk.NewInternalError(msg)
	}
	s.obs.ending(res.Summary, res.Blocker, res.Unverified, s.obs.streamed.String())
	return sdk.PromptResponse{StopReason: stopReason(res.Stop)}, nil
}

// stopReason maps the loop's stop (SPEC §4.3) to the ACP stop reason. StopError is
// handled before this, so it does not appear here.
func stopReason(s loop.StopReason) sdk.StopReason {
	switch s {
	case loop.StopMaxTurns:
		return sdk.StopReasonMaxTurnRequests
	case loop.StopMaxTokens:
		return sdk.StopReasonMaxTokens
	case loop.StopCancelled:
		return sdk.StopReasonCancelled
	default:
		return sdk.StopReasonEndTurn
	}
}

// taskOf is the text of a prompt. Text is read as it is, and a resource link is
// named by its URI. Other content is refused, since the agent takes text only.
func taskOf(blocks []sdk.ContentBlock) (string, error) {
	var parts []string
	for _, b := range blocks {
		switch {
		case b.Text != nil:
			parts = append(parts, b.Text.Text)
		case b.ResourceLink != nil:
			parts = append(parts, "["+b.ResourceLink.Uri+"]")
		default:
			return "", sdk.NewInvalidParams("this agent takes text prompts")
		}
	}
	task := strings.TrimSpace(strings.Join(parts, "\n"))
	if task == "" {
		return "", sdk.NewInvalidParams("the prompt is empty")
	}
	return task, nil
}
