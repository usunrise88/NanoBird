package model

import (
	"errors"
	"io"
)

// Reply is one whole model turn, collected from its stream.
type Reply struct {
	Text      string
	Reasoning string
	Calls     []ToolCall
	Finish    string
	Usage     Usage
}

// Collect reads a stream to its final event and returns the reply. It closes
// the stream.
func Collect(s Stream) (Reply, error) {
	return CollectWith(s, nil)
}

// CollectWith is Collect, and it also hands each piece of text to text as it
// arrives (SPEC §8.3). A nil text collects only.
func CollectWith(s Stream, text func(piece string)) (Reply, error) {
	defer func() { _ = s.Close() }()
	var r Reply
	for {
		ev, err := s.Next()
		if errors.Is(err, io.EOF) {
			return Reply{}, errors.New("model: the reply ended without a final event")
		}
		if err != nil {
			return Reply{}, err
		}
		switch {
		case ev.Final != nil:
			r.Finish = ev.Final.FinishReason
			r.Calls = ev.Final.ToolCalls
			r.Usage = ev.Final.Usage
			return r, nil
		case ev.Text != "":
			r.Text += ev.Text
			if text != nil {
				text(ev.Text)
			}
		case ev.Reasoning != "":
			r.Reasoning += ev.Reasoning
		}
	}
}
