package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/seungduk/understudy/record"
	"github.com/seungduk/understudy/script"
)

// record-proxy forwards to the real API and writes what the model answered as a
// script when it stops (Ctrl-C or SIGTERM). Credentials pass through, never kept.
func cmdRecordProxy(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("record-proxy", flag.ContinueOnError)
	fs.SetOutput(stderr)
	upstream := fs.String("upstream", "https://api.anthropic.com", "the API to forward to")
	addr := fs.String("addr", "127.0.0.1:8766", "address to listen on")
	out := fs.String("out", "", "the script file to write when the proxy stops")
	run := fs.String("run", "", "provenance: the run id")
	task := fs.String("task", "", "provenance: the task id")
	seat := fs.String("seat", "", "provenance: the seat")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *out == "" {
		fmt.Fprintln(stderr, "understudy record-proxy: give --out FILE for the recorded script")
		return 2
	}
	p, err := record.NewProxy(*upstream, nil)
	if err != nil {
		fmt.Fprintf(stderr, "understudy record-proxy: %v\n", err)
		return 2
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintf(stderr, "understudy record-proxy: cannot listen on %s: %v\n", *addr, err)
		return 1
	}
	started := time.Now()
	hs := &http.Server{Handler: p}
	fmt.Fprintf(stdout, "understudy record-proxy on http://%s -> %s; stop with Ctrl-C to write %s\n", ln.Addr(), *upstream, *out)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		_ = hs.Close()
	}()
	if err := hs.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(stderr, "understudy record-proxy: %v\n", err)
		return 1
	}
	f := p.File(record.Meta{Run: *run, Task: *task, Seat: *seat, RecordedAt: started})
	if len(f.Sessions) == 0 {
		fmt.Fprintln(stderr, "understudy record-proxy: nothing was recorded; no file written")
		return 1
	}
	b, err := script.Marshal(f)
	if err == nil {
		err = os.WriteFile(*out, b, 0o644)
	}
	if err != nil {
		fmt.Fprintf(stderr, "understudy record-proxy: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "wrote %s (%d session(s))\n", *out, len(f.Sessions))
	return 0
}
