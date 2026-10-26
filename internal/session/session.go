package session

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Log is the open transcript of one session. Each Write reaches the disk at
// once, so a killed run keeps every record it wrote.
type Log struct {
	id    string
	dir   string
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
	return &Log{id: id, dir: dir, path: path, file: f, clock: clock}, nil
}

// Open reopens the transcript of an existing session to append to it. Sequence
// numbers go on from the last record.
func Open(root, id string, clock func() time.Time) (*Log, error) {
	dir := filepath.Join(root, ".nanobird", "sessions", id)
	path := filepath.Join(dir, "transcript.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &Log{id: id, dir: dir, path: path, file: f, seq: bytes.Count(data, []byte("\n")), clock: clock}, nil
}

// Dir is the session directory, which holds the transcript and the checkpoint.
func (l *Log) Dir() string {
	return l.dir
}

// Seq is the sequence number of the last record written.
func (l *Log) Seq() int {
	return l.seq
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

// Entry is one record read back from a transcript.
type Entry struct {
	Seq  int             `json:"seq"`
	Time string          `json:"time"`
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}

// List returns the session ids under root, sorted by name, which sorts them by
// start time.
func List(root string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, ".nanobird", "sessions"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	return ids, nil
}

// Read returns every record of a transcript, in order.
func Read(root, id string) ([]Entry, error) {
	f, err := os.Open(filepath.Join(root, ".nanobird", "sessions", id, "transcript.jsonl"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return nil, fmt.Errorf("transcript %s: %w", id, err)
		}
		out = append(out, e)
	}
	return out, sc.Err()
}
