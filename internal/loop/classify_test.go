package loop

import (
	"context"
	"strings"
	"testing"

	"github.com/usunrise88/nanobird/internal/model/fake"
)

func TestClassifyChatAndTask(t *testing.T) {
	cases := map[string]Mode{
		"hello":              ModeChat,
		"thanks!":            ModeChat,
		"What is a closure?": ModeChat,
		"Explain the difference between a slice and an array": ModeChat,
		"What files are in this directory?":                   ModeTask,
		"Create a file named a.txt":                           ModeTask,
		"fix the failing test":                                ModeTask,
		"Why does main.go fail to build?":                     ModeTask,
		"":                                                    ModeTask,
	}
	for task, want := range cases {
		if got := Classify(task); got != want {
			t.Errorf("Classify(%q) = %s, want %s", task, got, want)
		}
	}
}

func TestChatPromptGetsOneReplyWithoutTools(t *testing.T) {
	h := newHarness(t, t.TempDir(), defaultConfig(), fake.Reply{Text: "A closure is a function that keeps the variables it sees."})
	res := h.loop.Run(context.Background(), "hello, what is a closure?")
	if res.Stop != StopEndTurn || res.Turns != 1 || !strings.Contains(res.Summary, "closure") {
		t.Fatalf("result = %+v", res)
	}
	if h.client.Requests[0].Tools != nil {
		t.Fatalf("a chat reply was offered %d tools", len(h.client.Requests[0].Tools))
	}
}

func TestWorkspaceQuestionIsATaskWithTools(t *testing.T) {
	h := newHarness(t, t.TempDir(), defaultConfig(), listDir(), finishWith("ok"), finishWith("ok"))
	res := h.loop.Run(context.Background(), "What files are in this directory?")
	if res.Stop != StopEndTurn || len(h.client.Requests[0].Tools) == 0 {
		t.Fatalf("result = %+v, tools = %d", res, len(h.client.Requests[0].Tools))
	}
}
