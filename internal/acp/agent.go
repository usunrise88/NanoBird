// Package acp is nanobird as an ACP agent: sessions, prompts and session updates
// over stdio (SPEC §8), on coder/acp-go-sdk. Nothing below it imports it (SPEC §3).
package acp

import (
	"context"
	"path/filepath"
	"strings"
	"sync"

	sdk "github.com/coder/acp-go-sdk"
	"github.com/usunrise88/nanobird/internal/loop"
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

// Open starts a session in a workspace. The observer hears what the session's
// prompts do, and the agent sends it to the client as session updates.
type Open func(workspace string, obs loop.Observer) (Runner, error)

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

// activeSession is one open session. cancel stops the prompt that runs, if one does.
type activeSession struct {
	runner Runner
	obs    *updates
	mu     sync.Mutex
	cancel context.CancelFunc
}

// Initialize answers the handshake: ACP v1, no authentication, and text prompts.
func (a *Agent) Initialize(context.Context, sdk.InitializeRequest) (sdk.InitializeResponse, error) {
	return sdk.InitializeResponse{
		ProtocolVersion: sdk.ProtocolVersionNumber,
		AgentInfo:       &sdk.Implementation{Name: "nanobird", Version: a.version},
		AuthMethods:     []sdk.AuthMethod{},
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
	obs := &updates{agent: a, root: p.Cwd}
	runner, err := a.open(p.Cwd, obs)
	if err != nil {
		return sdk.NewSessionResponse{}, sdk.NewInternalError(err.Error())
	}
	id := sdk.SessionId(runner.ID())
	obs.id = id
	a.mu.Lock()
	a.sessions[id] = &activeSession{runner: runner, obs: obs}
	a.mu.Unlock()
	return sdk.NewSessionResponse{SessionId: id}, nil
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
		return sdk.PromptResponse{}, sdk.NewInvalidRequest("a prompt is already running in this session")
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

// The session methods that are not built yet answer method not found, so that a
// client can tell them from a failure (SPEC §8.2).

// Logout has nothing to end.
func (a *Agent) Logout(context.Context, sdk.LogoutRequest) (sdk.LogoutResponse, error) {
	return sdk.LogoutResponse{}, sdk.NewMethodNotFound("logout")
}

// CloseSession is not built yet.
func (a *Agent) CloseSession(context.Context, sdk.CloseSessionRequest) (sdk.CloseSessionResponse, error) {
	return sdk.CloseSessionResponse{}, sdk.NewMethodNotFound("session/close")
}

// ListSessions is not built yet.
func (a *Agent) ListSessions(context.Context, sdk.ListSessionsRequest) (sdk.ListSessionsResponse, error) {
	return sdk.ListSessionsResponse{}, sdk.NewMethodNotFound("session/list")
}

// ResumeSession is not built yet.
func (a *Agent) ResumeSession(context.Context, sdk.ResumeSessionRequest) (sdk.ResumeSessionResponse, error) {
	return sdk.ResumeSessionResponse{}, sdk.NewMethodNotFound("session/resume")
}

// SetSessionConfigOption is not built yet.
func (a *Agent) SetSessionConfigOption(context.Context, sdk.SetSessionConfigOptionRequest) (sdk.SetSessionConfigOptionResponse, error) {
	return sdk.SetSessionConfigOptionResponse{}, sdk.NewMethodNotFound("session/set_config_option")
}

// SetSessionMode is not built yet.
func (a *Agent) SetSessionMode(context.Context, sdk.SetSessionModeRequest) (sdk.SetSessionModeResponse, error) {
	return sdk.SetSessionModeResponse{}, sdk.NewMethodNotFound("session/set_mode")
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

// begin records the cancel of a prompt that starts. It reports false when a
// prompt already runs, since a session runs one prompt at a time.
func (s *activeSession) begin(cancel context.CancelFunc) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return false
	}
	s.cancel = cancel
	return true
}

// end forgets the prompt that finished.
func (s *activeSession) end() {
	s.mu.Lock()
	s.cancel = nil
	s.mu.Unlock()
}

// stop cancels the prompt that runs, if any.
func (s *activeSession) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
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
	if res.Summary != "" && !strings.Contains(s.obs.streamed.String(), res.Summary) {
		s.obs.send(sdk.UpdateAgentMessageText(res.Summary))
	}
	if res.Blocker != "" {
		s.obs.send(sdk.UpdateAgentMessageText("Blocked: " + res.Blocker))
	}
	if res.Unverified {
		s.obs.send(sdk.UpdateAgentMessageText("The finish was accepted after repeated rejections, so it is unverified."))
	}
	return sdk.PromptResponse{StopReason: stopReason(res.Stop)}, nil
}

// stopReason maps the loop's stop (SPEC §4.3) to the ACP stop reason. StopError
// is handled before this, so it does not appear here.
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
