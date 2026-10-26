package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/usunrise88/nanobird/internal/mech"
	"github.com/usunrise88/nanobird/internal/safety"
)

// Shell is the interpreter run_shell hands each command to (SPEC §6.2).
type Shell struct {
	// Name is how the prefill names the shell.
	Name string
	// Argv is the program and its fixed arguments. The command text follows.
	Argv []string
}

// DetectShell picks the shell of this machine: PowerShell on Windows, bash
// elsewhere.
func DetectShell() Shell {
	if runtime.GOOS == "windows" {
		args := []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command"}
		if p, err := exec.LookPath("pwsh"); err == nil {
			return Shell{Name: "PowerShell 7", Argv: append([]string{p}, args...)}
		}
		if p, err := exec.LookPath("powershell"); err == nil {
			return Shell{Name: "Windows PowerShell 5.1", Argv: append([]string{p}, args...)}
		}
		return Shell{Name: "cmd", Argv: []string{"cmd", "/C"}}
	}
	if p, err := exec.LookPath("bash"); err == nil {
		return Shell{Name: "bash", Argv: []string{p, "-c"}}
	}
	return Shell{Name: "sh", Argv: []string{"sh", "-c"}}
}

var runShellTool = Tool{
	Name:        "run_shell",
	Description: "Run one command in the workspace root, in the shell named in the prompt. Output and the exit code come back. Commands that are refused or need permission do not run.",
	Parameters: schema(`{
		"command": {"type": "string", "description": "The command text"},
		"timeout_s": {"type": "integer", "description": "Seconds before the command is stopped, 120 by default"}
	}`, "command"),
	run: runRunShell,
}

// deniedError is the error of a call that needs a permission it did not get. A run
// with no client to ask says so.
func deniedError(env *Env, need string) error {
	if env.Permit == nil {
		return fmt.Errorf("%s; a run without a permission prompt denies it", need)
	}
	return fmt.Errorf("%s; the client did not allow it", need)
}

func runRunShell(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	var a struct {
		Command  string `json:"command"`
		TimeoutS int    `json:"timeout_s"`
	}
	if err := decode(args, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.Command) == "" {
		return "", errors.New("command is empty")
	}
	if !env.Unsafe {
		switch d := safety.AssessShell(a.Command); d.Level {
		case safety.Refuse:
			return "", fmt.Errorf("refused by safety ring 1: %s", d.Reason)
		case safety.Ask:
			p := Permission{Tool: "run_shell", Command: a.Command, Reason: d.Reason}
			if d.AllowAlways {
				p.Family = family(a.Command)
			}
			if !env.permit(ctx, p) {
				return "", deniedError(env, "needs permission: "+d.Reason)
			}
		}
	}
	if env.Switches.On(mech.Boundary) {
		if name, ok := safety.TouchesSensitive(a.Command); ok {
			p := Permission{Tool: "run_shell", Command: a.Command, Reason: "the command names " + name + ", a credential location"}
			if !env.permit(ctx, p) {
				return "", deniedError(env, "needs permission: the command names "+name+", a credential location")
			}
		}
	}
	timeout := env.ShellTimeout
	if a.TimeoutS > 0 {
		timeout = time.Duration(a.TimeoutS) * time.Second
	}
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	argv := append(append([]string{}, env.Shell.Argv[1:]...), a.Command)
	cmd := exec.CommandContext(ctx, env.Shell.Argv[0], argv...)
	cmd.Dir = env.Workspace.Root()
	// A child that keeps the pipe open must not hold the run after the shell is stopped.
	cmd.WaitDelay = 2 * time.Second
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	runErr := cmd.Run()
	// Output that is not UTF-8 is shown with replacement characters (SPEC §5.7).
	text := strings.ToValidUTF8(out.String(), "�")
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("timed out after %s; output so far:\n%s", timeout, text)
	}
	var exit *exec.ExitError
	switch {
	case errors.As(runErr, &exit):
		return fmt.Sprintf("%s\n[exit code %d]", text, exit.ExitCode()), nil
	case runErr != nil:
		return "", runErr
	case strings.TrimSpace(text) == "":
		return "(no output)\n[exit code 0]", nil
	}
	return fmt.Sprintf("%s\n[exit code 0]", text), nil
}
