// Command nanobird is the NanoBird agent harness (SPEC §11.2).
//
//	nanobird acp               ACP agent over stdio
//	nanobird run --task FILE   headless run of one task
//	nanobird doctor            engine probes and config check
//	nanobird sessions          list and show local sessions
//	nanobird version           version, commit and prefill budget
package main

import (
	"fmt"
	"os"

	"github.com/usunrise88/nanobird/internal/prefill"
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
		fmt.Printf("prefill budget: %d bytes (SPEC §5.1)\n", prefill.BudgetBytes)
	case "run":
		os.Exit(runTask(os.Args[2:], os.Stdout, os.Stderr))
	case "doctor":
		os.Exit(doctorCmd(os.Args[2:], os.Stdout, os.Stderr))
	case "sessions":
		os.Exit(sessionsCmd(os.Args[2:], os.Stdout, os.Stderr))
	case "acp":
		os.Exit(acpCommand(os.Args[2:], os.Stdin, os.Stdout, os.Stderr, version))
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: nanobird acp | run | doctor | sessions | version")
}
