// Command nanobird is the NanoBird agent harness (SPEC §11.2).
//
//	nanobird acp               ACP agent over stdio
//	nanobird run --task FILE   headless run of one task
//	nanobird run --resume ID   resume a session from its last checkpoint
//	nanobird doctor            engine probes and config check
//	nanobird sessions          list and show local sessions
//	nanobird version           version and commit
package main

import (
	"fmt"
	"os"
)

// version and commit are stamped by the linker (see the Makefile):
// -ldflags "-X main.version=... -X main.commit=...".
var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch verb := os.Args[1]; verb {
	case "version":
		fmt.Printf("nanobird %s (commit %s)\n", version, commit)
	case "run":
		os.Exit(runTask(os.Args[2:], os.Stdout, os.Stderr))
	case "acp", "doctor", "sessions":
		fmt.Fprintf(os.Stderr, "nanobird: %s is not implemented yet\n", verb)
		os.Exit(1)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: nanobird acp | run | doctor | sessions | version")
}
