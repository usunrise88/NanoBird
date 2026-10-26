// Command model is a scripted engine for the ACP test kit. It speaks the OpenAI
// dialect that nanobird's client uses. A request that carries tools gets one finish
// call, as the e2e test does, and a request without tools gets the text "ok". Every
// reply waits for -delay first, so that a cancel can land on a request in flight. It
// stands in for an engine; it is not a model.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"
)

const modelName = "scripted"

// finish is the reply to a request that carries tools.
var finish = []string{
	`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"finish","arguments":"{\"summary\":\"Done.\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`,
}

// text is the reply to a request without tools.
var text = []string{
	`{"choices":[{"delta":{"content":"ok"}}]}`,
	`{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":1}}`,
}

func main() {
	addr := flag.String("addr", "127.0.0.1:18081", "address to listen on")
	delay := flag.Duration("delay", time.Second, "wait before each reply")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"object":"list","data":[{"id":%q,"object":"model"}]}`, modelName)
	})
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Tools []json.RawMessage `json:"tools"`
		}
		// A body that is not JSON counts as a request without tools.
		_ = json.NewDecoder(r.Body).Decode(&req)
		time.Sleep(*delay)
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := text
		if len(req.Tools) > 0 {
			chunks = finish
		}
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	})
	log.Fatal(http.ListenAndServe(*addr, mux))
}
