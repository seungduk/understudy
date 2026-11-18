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

	"github.com/seungduk/understudy/script"
	"github.com/seungduk/understudy/server"
)

func cmdServe(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", "127.0.0.1:8765", "address to listen on (port 0 picks a free port)")
	file := fs.String("script", "", "script file to load at start")
	clk := fs.String("clock", "real", "clock: real or virtual")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	o := server.Options{}
	switch *clk {
	case "real":
	case "virtual":
		o.Clock = server.VirtualClock
	default:
		fmt.Fprintf(stderr, "understudy serve: unknown --clock %q. Fix: use real or virtual\n", *clk)
		return 2
	}
	if *file != "" {
		sc, err := script.Load(*file)
		if err != nil {
			fmt.Fprintf(stderr, "understudy serve: %s: %v\n", *file, err)
			return 1
		}
		o.Script = sc
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintf(stderr, "understudy serve: cannot listen on %s: %v. Fix: pick a free address with --addr\n", *addr, err)
		return 1
	}
	s := server.New(o)
	hs := &http.Server{Handler: s.Handler()}
	fmt.Fprintf(stdout, "understudy %s listening on http://%s\n", server.Version, ln.Addr())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		_ = hs.Close()
	}()
	if err := hs.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(stderr, "understudy serve: %v\n", err)
		return 1
	}
	return 0
}
