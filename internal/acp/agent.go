// Package acp is nanobird as an ACP agent: sessions, prompts and session updates
// over stdio (SPEC §8), on coder/acp-go-sdk. Nothing below it imports it (SPEC §3).
package acp

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	sdk "github.com/coder/acp-go-sdk"
	"github.com/usunrise88/nanobird/internal/loop"
	"github.com/usunrise88/nanobird/internal/mcp"
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
	// SetMode sets the mode of the prompts that follow: auto, chat or task (SPEC §8.4).
	SetMode(mode string)
	// SetAblation sets the mechanisms that the session leaves off (SPEC §8.8).
	SetAblation(mechanisms []string) error
	// Notices are the things the client should hear about the session before its
	// first prompt, such as an MCP server that did not start (SPEC §8.3).
	Notices() []string
	// Offline is the offline option: web tools and URL servers are not assembled
	// (SPEC §8.4). SetOffline changes it, and the MCP servers start again.
	Offline() bool
	SetOffline(offline bool) error
	// Model is the engine's model name. Models lists the names the engine serves, and
	// SetModel changes the model of the prompts that follow (SPEC §8.4).
	Model() string
	SetModel(name string) error
	Models(ctx context.Context) ([]string, error)
	// Thinking is the thinking policy of the next prompts: off, plan or on.
	Thinking() string
	// SetThinking sets the thinking policy of the next prompts (SPEC §4.5, §8.4).
	SetThinking(mode string)
	// Close releases what the session holds.
	Close()
}

// Open starts a session in a workspace. An id that is not empty names a session of
// that workspace to continue, from its transcript and its last checkpoint. The
// servers are the MCP servers that the client passed to the session (SPEC §6.4).
// The observer hears what the session's prompts do.
type Open func(workspace, id string, servers map[string]mcp.Spec, obs loop.Observer) (Runner, error)

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
	// known maps every session this process has seen to its workspace, since a
	// delete request carries no workspace (SPEC §8.2).
	known map[sdk.SessionId]string
	// caps is what the client offers, from its initialize request (SPEC §8.6).
	caps sdk.ClientCapabilities
}

// NewAgent returns an agent that opens its sessions with open. Serve connects it
// to a client.
func NewAgent(open Open, version string) *Agent {
	return &Agent{open: open, version: version, ready: make(chan struct{}), sessions: map[sdk.SessionId]*activeSession{}, known: map[sdk.SessionId]string{}}
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
	// mode is the mode the session's prompts run in, one of the modes below.
	mode string
	// thinking is the thinking policy for the session's next prompt.
	thinking string
	// titled is set once the session has a title, from its first task.
	titled bool
	// prompted is set once a prompt has run, after which the ablation is fixed.
	prompted bool
	// closed is closed once the runner is closed, so that a delete can wait for it.
	closed    chan struct{}
	closeOnce sync.Once
	// notices are sent as messages before the first prompt runs.
	notices []string
	// offline and model are the settings for the next prompt. applied holds the
	// values the runner has, so that only a change reaches it.
	offline, appliedOffline bool
	model, appliedModel     string
	// models are the model names the engine serves, when it lists them.
	models []string
}

// thinkingValues are the values of the thinking option (SPEC §4.5, §8.4).
var thinkingValues = []string{"off", "plan", "on"}

// modes are the modes a session offers (SPEC §8.4). Auto is the default.
var modes = []struct{ id, name, description string }{
	{"auto", "Auto", "The classifier of SPEC §4.1 chooses chat or task for each prompt."},
	{"chat", "Chat", "Each prompt gets one reply, and no tools."},
	{"task", "Task", "Each prompt runs the full loop with the tools."},
}

// Initialize answers the handshake: ACP v1, no authentication, text prompts, and the
// session methods that this agent serves. It keeps what the client offers.
func (a *Agent) Initialize(_ context.Context, p sdk.InitializeRequest) (sdk.InitializeResponse, error) {
	a.mu.Lock()
	a.caps = p.ClientCapabilities
	a.mu.Unlock()
	return sdk.InitializeResponse{
		ProtocolVersion: sdk.ProtocolVersionNumber,
		AgentInfo:       &sdk.Implementation{Name: "nanobird", Version: a.version},
		AgentCapabilities: sdk.AgentCapabilities{
			LoadSession: true,
			SessionCapabilities: sdk.SessionCapabilities{
				Close:  &sdk.SessionCloseCapabilities{},
				Delete: &sdk.SessionDeleteCapabilities{},
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
	s, err := a.start(p.Cwd, "", p.McpServers)
	if err != nil {
		return sdk.NewSessionResponse{}, err
	}
	return sdk.NewSessionResponse{SessionId: s.id, Modes: modeState(s.currentMode()), ConfigOptions: s.options()}, nil
}

// LoadSession continues a session of the workspace, and replays its history to the
// client as session updates before it answers (SPEC §8.2).
func (a *Agent) LoadSession(_ context.Context, p sdk.LoadSessionRequest) (sdk.LoadSessionResponse, error) {
	s, err := a.attach(p.Cwd, p.SessionId, p.McpServers)
	if err != nil {
		return sdk.LoadSessionResponse{}, err
	}
	entries, err := session.Read(p.Cwd, string(p.SessionId))
	if err != nil {
		return sdk.LoadSessionResponse{}, sdk.NewInternalError(err.Error())
	}
	s.obs.replay(entries)
	return sdk.LoadSessionResponse{Modes: modeState(s.currentMode()), ConfigOptions: s.options()}, nil
}

// ResumeSession continues a session of the workspace without replaying its history
// (SPEC §8.2).
func (a *Agent) ResumeSession(_ context.Context, p sdk.ResumeSessionRequest) (sdk.ResumeSessionResponse, error) {
	s, err := a.attach(p.Cwd, p.SessionId, p.McpServers)
	if err != nil {
		return sdk.ResumeSessionResponse{}, err
	}
	return sdk.ResumeSessionResponse{Modes: modeState(s.currentMode()), ConfigOptions: s.options()}, nil
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
	a.mu.Lock()
	for _, id := range ids {
		a.known[sdk.SessionId(id)] = *p.Cwd
	}
	a.mu.Unlock()
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

// UnstableDeleteSession deletes a session of the agent (SPEC §8.2). An open session is
// closed first, and cancelled if a prompt runs. Its transcript and log move to the
// trash of the workspace, so that the deletion can be undone by hand (SPEC §5.6).
// A session that is gone, or that the agent has not listed, is not an error: the
// delete changes nothing, as the ACP rule for delete says. The agent moves only the
// sessions of the workspaces it has listed or opened.
func (a *Agent) UnstableDeleteSession(_ context.Context, p sdk.UnstableDeleteSessionRequest) (sdk.UnstableDeleteSessionResponse, error) {
	if !session.IsName(string(p.SessionId)) {
		return sdk.UnstableDeleteSessionResponse{}, sdk.NewInvalidParams("not a session id: " + string(p.SessionId))
	}
	a.mu.Lock()
	root, known := a.known[p.SessionId]
	s, open := a.sessions[p.SessionId]
	delete(a.sessions, p.SessionId)
	a.mu.Unlock()
	if open {
		s.close()
		<-s.closed
	}
	if !known {
		return sdk.UnstableDeleteSessionResponse{}, nil
	}
	if err := session.Trash(root, string(p.SessionId), time.Now()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return sdk.UnstableDeleteSessionResponse{}, sdk.NewInternalError(err.Error())
	}
	a.mu.Lock()
	delete(a.known, p.SessionId)
	a.mu.Unlock()
	return sdk.UnstableDeleteSessionResponse{}, nil
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
	if err := s.applyConfig(); err != nil {
		return sdk.PromptResponse{}, sdk.NewInternalError(err.Error())
	}
	for _, n := range s.takeNotices() {
		s.obs.send(sdk.UpdateAgentMessageText(n))
	}
	s.runner.SetMode(s.currentMode())
	s.runner.SetThinking(s.currentThinking())
	if s.claimTitle() {
		s.obs.title(task)
	}
	s.obs.streamed.Reset()
	res := s.runner.Prompt(run, task)
	return a.answer(s, res)
}

// HandleExtensionMethod answers the nanobird extension methods (SPEC §8.8).
// _nanobird/stats reports the counters of a session, and _nanobird/ablation sets the
// mechanisms that it leaves off, before its first prompt. Other methods are not
// found.
func (a *Agent) HandleExtensionMethod(_ context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case "_nanobird/stats", "_nanobird/ablation":
	default:
		return nil, sdk.NewMethodNotFound(method)
	}
	var p struct {
		SessionId  sdk.SessionId `json:"sessionId"`
		Mechanisms []string      `json:"mechanisms"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, sdk.NewInvalidParams(err.Error())
	}
	s, err := a.session(p.SessionId)
	if err != nil {
		return nil, err
	}
	if method == "_nanobird/stats" {
		return s.obs.stats(), nil
	}
	if err := s.setAblation(p.Mechanisms); err != nil {
		return nil, err
	}
	return map[string]any{}, nil
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

// SetSessionConfigOption sets a config option of a session (SPEC §8.4): thinking
// (off, plan or on), model, or offline. A value applies from the next prompt. The
// answer lists the options with the values now set.
func (a *Agent) SetSessionConfigOption(_ context.Context, p sdk.SetSessionConfigOptionRequest) (sdk.SetSessionConfigOptionResponse, error) {
	if p.ValueId == nil {
		return sdk.SetSessionConfigOptionResponse{}, sdk.NewInvalidParams("no such config option")
	}
	s, err := a.session(p.ValueId.SessionId)
	if err != nil {
		return sdk.SetSessionConfigOptionResponse{}, err
	}
	value := string(p.ValueId.Value)
	switch p.ValueId.ConfigId {
	case "thinking":
		if !slices.Contains(thinkingValues, value) {
			return sdk.SetSessionConfigOptionResponse{}, sdk.NewInvalidParams("no such thinking value: " + value)
		}
		s.setThinking(value)
	case "model":
		if err := s.setModel(value); err != nil {
			return sdk.SetSessionConfigOptionResponse{}, err
		}
	case "offline":
		if value != "on" && value != "off" {
			return sdk.SetSessionConfigOptionResponse{}, sdk.NewInvalidParams("no such offline value: " + value)
		}
		s.setOffline(value == "on")
	default:
		return sdk.SetSessionConfigOptionResponse{}, sdk.NewInvalidParams("no such config option")
	}
	return sdk.SetSessionConfigOptionResponse{ConfigOptions: s.options()}, nil
}

// SetSessionMode sets the mode of the session's prompts: auto, chat or task (SPEC
// §8.4). The mode applies from the next prompt.
func (a *Agent) SetSessionMode(_ context.Context, p sdk.SetSessionModeRequest) (sdk.SetSessionModeResponse, error) {
	s, err := a.session(p.SessionId)
	if err != nil {
		return sdk.SetSessionModeResponse{}, err
	}
	for _, m := range modes {
		if m.id == string(p.ModeId) {
			s.setMode(m.id)
			return sdk.SetSessionModeResponse{}, nil
		}
	}
	return sdk.SetSessionModeResponse{}, sdk.NewInvalidParams("no such mode: " + string(p.ModeId))
}

// start opens a session in the workspace at cwd, continuing the session id when it
// is not empty, with the MCP servers the client passed. It registers the session
// under its id.
func (a *Agent) start(cwd, id string, list []sdk.McpServer) (*activeSession, error) {
	servers, notes := mcpSpecs(list)
	obs := &updates{agent: a, root: cwd, started: time.Now()}
	runner, err := a.open(cwd, id, servers, obs)
	if err != nil {
		return nil, sdk.NewInternalError(err.Error())
	}
	s := &activeSession{
		id: sdk.SessionId(runner.ID()), runner: runner, obs: obs, mode: "auto",
		thinking: runner.Thinking(), titled: id != "", notices: append(runner.Notices(), notes...),
		offline: runner.Offline(), appliedOffline: runner.Offline(),
		model: runner.Model(), appliedModel: runner.Model(),
		models: listModels(runner),
		closed: make(chan struct{}),
	}
	obs.id = s.id
	a.mu.Lock()
	a.sessions[s.id] = s
	a.known[s.id] = cwd
	a.mu.Unlock()
	return s, nil
}

// attach returns the session with id, opening it from its transcript with the MCP
// servers the client passed when it is not open yet. An open session keeps the
// servers it started with.
func (a *Agent) attach(cwd string, id sdk.SessionId, list []sdk.McpServer) (*activeSession, error) {
	if !filepath.IsAbs(cwd) {
		return nil, sdk.NewInvalidParams("cwd must be an absolute path")
	}
	a.mu.Lock()
	s, ok := a.sessions[id]
	a.mu.Unlock()
	if ok {
		return s, nil
	}
	return a.start(cwd, string(id), list)
}

// mcpSpecs turns the MCP servers that a session request carries into the servers the
// session starts. Stdio and streamable HTTP servers start. A server with another
// transport does not, and the client hears why (SPEC §6.4).
func mcpSpecs(list []sdk.McpServer) (map[string]mcp.Spec, []string) {
	specs := map[string]mcp.Spec{}
	var notes []string
	for _, m := range list {
		switch {
		case m.Stdio != nil:
			env := map[string]string{}
			for _, v := range m.Stdio.Env {
				env[v.Name] = v.Value
			}
			specs[m.Stdio.Name] = mcp.Spec{Command: m.Stdio.Command, Args: m.Stdio.Args, Env: env}
		case m.Http != nil:
			headers := map[string]string{}
			for _, h := range m.Http.Headers {
				headers[h.Name] = h.Value
			}
			specs[m.Http.Name] = mcp.Spec{URL: m.Http.Url, Headers: headers}
		case m.Sse != nil:
			notes = append(notes, "MCP server "+m.Sse.Name+" uses the SSE transport, which NanoBird does not start")
		case m.Acp != nil:
			notes = append(notes, "MCP server "+m.Acp.Name+" uses the ACP transport, which NanoBird does not start")
		}
	}
	return specs, notes
}

// clientCaps is what the client offers.
func (a *Agent) clientCaps() sdk.ClientCapabilities {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.caps
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
	s.prompted = true
	return true
}

// end forgets the prompt that finished, and closes the runner if the session was
// closed while the prompt ran.
func (s *activeSession) end() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancel = nil
	if s.closing {
		s.finish()
	}
}

// finish closes the runner, once, and says so.
func (s *activeSession) finish() {
	s.closeOnce.Do(func() {
		s.runner.Close()
		close(s.closed)
	})
}

// currentMode is the mode the session's prompts run in.
func (s *activeSession) currentMode() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mode
}

// setMode sets the mode that the session's next prompt runs in.
func (s *activeSession) setMode(mode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mode = mode
}

// currentThinking is the thinking policy of the session's next prompt.
func (s *activeSession) currentThinking() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.thinking
}

// setAblation sets the mechanisms the session leaves off. It is refused once a
// prompt has run (SPEC §8.8).
func (s *activeSession) setAblation(mechanisms []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.prompted {
		return sdk.NewInvalidRequest("the ablation is set before the first prompt")
	}
	if err := s.runner.SetAblation(mechanisms); err != nil {
		return sdk.NewInvalidParams(err.Error())
	}
	return nil
}

// setThinking sets the thinking policy of the session's next prompt.
func (s *activeSession) setThinking(mode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.thinking = mode
}

// takeNotices returns the notices that are still to be sent, and forgets them.
func (s *activeSession) takeNotices() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.notices
	s.notices = nil
	return n
}

// claimTitle reports whether the session still needs its title, and marks it as
// titled.
func (s *activeSession) claimTitle() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.titled {
		return false
	}
	s.titled = true
	return true
}

// configOptions are the config options of a session (SPEC §8.4): thinking, offline,
// and the model when the engine lists its models.
func configOptions(thinking string, offline bool, model string, models []string) []sdk.SessionConfigOption {
	values := sdk.SessionConfigSelectOptionsUngrouped{
		{Name: "Off", Value: "off"},
		{Name: "Plan", Value: "plan"},
		{Name: "On", Value: "on"},
	}
	think := sdk.NewSessionConfigOptionSelect(sdk.SessionConfigValueId(thinking), sdk.SessionConfigSelectOptions{Ungrouped: &values})
	think.Select.Id = "thinking"
	think.Select.Name = "Thinking"
	desc := "Whether the model thinks: off, on for every turn, or plan, which thinks on the first turn and after a finish rejection (SPEC §4.5)."
	think.Select.Description = &desc
	opts := []sdk.SessionConfigOption{think}

	// Offline is a select: the ACP rule allows a boolean option only when the client
	// advertised booleans, and the SDK does not say whether it did.
	onOff := sdk.SessionConfigSelectOptionsUngrouped{{Name: "Off", Value: "off"}, {Name: "On", Value: "on"}}
	current := "off"
	if offline {
		current = "on"
	}
	local := sdk.NewSessionConfigOptionSelect(sdk.SessionConfigValueId(current), sdk.SessionConfigSelectOptions{Ungrouped: &onOff})
	local.Select.Id = "offline"
	local.Select.Name = "Offline"
	opts = append(opts, local)

	if len(models) > 0 {
		// The current model is always one of the choices, even when the engine does
		// not list it, so the option never shows a value that it does not offer.
		if !slices.Contains(models, model) {
			models = append([]string{model}, models...)
		}
		var names sdk.SessionConfigSelectOptionsUngrouped
		for _, m := range models {
			names = append(names, sdk.SessionConfigSelectOption{Name: m, Value: sdk.SessionConfigValueId(m)})
		}
		pick := sdk.NewSessionConfigOptionSelect(sdk.SessionConfigValueId(model), sdk.SessionConfigSelectOptions{Ungrouped: &names})
		pick.Select.Id = "model"
		pick.Select.Name = "Model"
		opts = append(opts, pick)
	}
	return opts
}

// listModels asks the engine for its model names. A failure leaves the option out.
func listModels(r Runner) []string {
	ctx, cancel := context.WithTimeout(context.Background(), modelListWait)
	defer cancel()
	models, err := r.Models(ctx)
	if err != nil {
		return nil
	}
	return models
}

// modelListWait bounds the wait for the engine's model names when a session opens.
const modelListWait = 5 * time.Second

// options are the config options of the session, with the values it has now.
func (s *activeSession) options() []sdk.SessionConfigOption {
	s.mu.Lock()
	thinking, offline, model, models := s.thinking, s.offline, s.model, s.models
	s.mu.Unlock()
	return configOptions(thinking, offline, model, models)
}

// setOffline and setModel set the options for the next prompt. A model must be one
// that the engine serves, when it lists them.
func (s *activeSession) setOffline(offline bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.offline = offline
}

func (s *activeSession) setModel(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.models) > 0 && !slices.Contains(s.models, name) {
		return sdk.NewInvalidParams("no such model: " + name)
	}
	s.model = name
	return nil
}

// applyConfig gives the runner the settings that changed since the last prompt, so
// the next prompt runs with them (SPEC §8.4). Only the prompt goroutine calls it.
func (s *activeSession) applyConfig() error {
	s.mu.Lock()
	offline, model := s.offline, s.model
	s.mu.Unlock()
	if offline != s.appliedOffline {
		if err := s.runner.SetOffline(offline); err != nil {
			return err
		}
		s.appliedOffline = offline
	}
	if model != s.appliedModel {
		if err := s.runner.SetModel(model); err != nil {
			return err
		}
		s.appliedModel = model
	}
	return nil
}

// modeState is the modes of a session, with the one it runs in.
func modeState(current string) *sdk.SessionModeState {
	state := &sdk.SessionModeState{CurrentModeId: sdk.SessionModeId(current)}
	for _, m := range modes {
		desc := m.description
		state.AvailableModes = append(state.AvailableModes, sdk.SessionMode{Id: sdk.SessionModeId(m.id), Name: m.name, Description: &desc})
	}
	return state
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
	s.finish()
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
