package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Log is the open transcript of one session. Each Write reaches the disk at
// once, so a killed run keeps every record it wrote.
type Log struct {
	id    string
	path  string
	file  *os.File
	seq   int
	clock func() time.Time
}

// Create opens a new transcript at .nanobird/sessions/<id>/transcript.jsonl
// under root. clock stamps every record; tests pass a fixed one.
func Create(root, id string, clock func() time.Time) (*Log, error) {
	dir := filepath.Join(root, ".nanobird", "sessions", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "transcript.jsonl")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, err
	}
	return &Log{id: id, path: path, file: f, clock: clock}, nil
}

// ID is the session id.
func (l *Log) ID() string {
	return l.id
}

// Path is the transcript file on disk.
func (l *Log) Path() string {
	return l.path
}

// Write appends one record: its sequence number, time, kind and data.
func (l *Log) Write(kind string, data any) error {
	l.seq++
	line, err := json.Marshal(record{
		Seq:  l.seq,
		Time: l.clock().UTC().Format(time.RFC3339Nano),
		Kind: kind,
		Data: data,
	})
	if err != nil {
		return fmt.Errorf("transcript: encode %s: %w", kind, err)
	}
	if _, err := l.file.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("transcript: write %s: %w", kind, err)
	}
	return nil
}

// Close closes the transcript file.
func (l *Log) Close() error {
	return l.file.Close()
}

type record struct {
	Seq  int    `json:"seq"`
	Time string `json:"time"`
	Kind string `json:"kind"`
	Data any    `json:"data"`
}

// NewID returns a session id that sorts by start time and is unlikely to
// collide with another run started in the same second.
func NewID(now time.Time) string {
	var b [3]byte
	_, _ = rand.Read(b[:]) // crypto/rand does not fail on the supported systems
	return now.UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}
