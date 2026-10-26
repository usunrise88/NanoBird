package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/usunrise88/nanobird/internal/doctor"
)

// doctorCmd is `nanobird doctor` (SPEC §7.4): the engine probes against the
// configured endpoint. It exits 0 only when no probe fails.
func doctorCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	flags := addEngineFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, err := flags.load()
	if err != nil {
		fmt.Fprintf(stderr, "nanobird doctor: %v\n", err)
		return 2
	}
	client, err := flags.client(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "nanobird doctor: %v\n", err)
		return 2
	}
	fmt.Fprintf(stdout, "engine: %s, model: %s\n", cfg.Model.Endpoint, cfg.Model.Name)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	code := 0
	for _, r := range doctor.Run(ctx, client, cfg.Model.Name) {
		fmt.Fprintf(stdout, "%-5s %-34s %s\n", strings.ToUpper(string(r.Status)), r.Name, r.Detail)
		if r.Status == doctor.Fail {
			code = 1
		}
	}
	return code
}
