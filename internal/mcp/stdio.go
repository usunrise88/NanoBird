package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// stdio is a connection over the standard input and output of a server that runs
// as a command (SPEC §6.4). The server's stderr is discarded, so a chatty server
// cannot fill the run's output.
type stdio struct {
	name  string
	cmd   *exec.Cmd
	stdin io.WriteCloser
	inbox chan rpc
}

// startStdio launches the command of a server. The server lives as long as the
// connection, not as long as the start-up context: close stops it.
func startStdio(name string, spec Spec) (*stdio, error) {
	cmd := exec.CommandContext(context.Background(), spec.Command, spec.Args...)
	cmd.Env = os.Environ()
	for k, v := range spec.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp server %s: %w", name, err)
	}
	s := &stdio{name: name, cmd: cmd, stdin: stdin, inbox: make(chan rpc, 64)}
	go s.read(stdout)
	return s, nil
}

// roundTrip writes the request, then reads the inbox until the reply with its id.
func (s *stdio) roundTrip(ctx context.Context, req rpc, answer func(context.Context, rpc)) (rpc, error) {
	if err := s.send(ctx, req); err != nil {
		return rpc{}, err
	}
	for {
		select {
		case <-ctx.Done():
			return rpc{}, ctx.Err()
		case m, ok := <-s.inbox:
			if !ok {
				return rpc{}, errors.New("the connection closed")
			}
			if m.Method != "" {
				if len(m.ID) > 0 {
					answer(ctx, m)
				}
				continue
			}
			if sameID(m.ID, req.ID) {
				return m, nil
			}
		}
	}
}

// send writes one message, one line each.
func (s *stdio) send(_ context.Context, m rpc) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if _, err := s.stdin.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("mcp server %s: %w", s.name, err)
	}
	return nil
}

// negotiated does nothing: stdio sends no protocol version header.
func (s *stdio) negotiated(string) {}

// close stops the server.
func (s *stdio) close() {
	_ = s.stdin.Close()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	_ = s.cmd.Wait()
}

// read pumps the server's messages into the inbox, one per line.
func (s *stdio) read(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var m rpc
		if json.Unmarshal(sc.Bytes(), &m) == nil {
			s.inbox <- m
		}
	}
	close(s.inbox)
}
