package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/seungduk/understudy/control"
	"github.com/seungduk/understudy/mcpserver"
	"github.com/seungduk/understudy/server"
	"github.com/seungduk/understudy/ui"
)

// serverMode is how understudy mcp finds the understudy server its tools act on.
type serverMode int

const (
	// autoServer uses the server at --url when one answers, else starts one there.
	autoServer serverMode = iota
	// attachServer only uses a running server.
	attachServer
	// startServer always starts one at --url.
	startServer
)

func cmdMCP(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	base := fs.String("url", "http://127.0.0.1:8765", "the understudy server the tools act on")
	mode := fs.String("server", "auto", "auto (use the server at --url, or start one there), attach (only use a running one) or start (always start one)")
	clk := fs.String("clock", "real", "the clock of a server this command starts: real or virtual")
	file := fs.String("script", "", "a script for a server this command starts to load at start")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	var m serverMode
	switch *mode {
	case "auto":
		m = autoServer
	case "attach":
		m = attachServer
	case "start":
		m = startServer
	default:
		fmt.Fprintf(stderr, "understudy mcp: unknown --server %q. Fix: use auto, attach or start\n", *mode)
		return 2
	}
	c, err := control.New(*base, nil)
	if err != nil {
		fmt.Fprintf(stderr, "understudy mcp: %v\n", err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	running := false
	if m != startServer {
		hctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		_, err := c.Health(hctx)
		cancel()
		running = err == nil
		if !running && m == attachServer {
			fmt.Fprintf(stderr, "understudy mcp: no understudy server answers at %s. Fix: start one with 'understudy serve --addr %s', or use --server auto\n", c.URL(), hostPort(c.URL()))
			return 1
		}
	}
	if running {
		fmt.Fprintf(stderr, "understudy mcp: using the understudy server at %s\n", c.URL())
	} else {
		o, code := serverOptions("mcp", *clk, *file, stderr)
		if code != 0 {
			return code
		}
		ln, err := net.Listen("tcp", hostPort(c.URL()))
		if err != nil {
			fmt.Fprintf(stderr, "understudy mcp: cannot start a server at %s: %v. Fix: give a free address with --url, or stop what holds it\n", c.URL(), err)
			return 1
		}
		s := server.New(o)
		h, _ := mount(s, on, off, selfURL(ln))
		hs := &http.Server{Handler: h}
		go func() { _ = hs.Serve(ln) }()
		defer func() {
			s.Reset()
			_ = hs.Close()
		}()
		if c, err = control.New(selfURL(ln), nil); err != nil {
			fmt.Fprintf(stderr, "understudy mcp: %v\n", err)
			return 1
		}
		fmt.Fprintf(stderr, "understudy mcp: started an understudy server at %s (web UI %s%s); it stops with this command\n", c.URL(), c.URL(), ui.Prefix)
	}
	ms := mcpserver.New(mcpserver.Options{Control: c})
	err = ms.Run(ctx, &mcp.IOTransport{Reader: io.NopCloser(stdin), Writer: nopWriteCloser{stdout}})
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) {
		fmt.Fprintf(stderr, "understudy mcp: %v\n", err)
		return 1
	}
	return 0
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

func hostPort(base string) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	if u.Port() == "" {
		return net.JoinHostPort(u.Hostname(), "80")
	}
	return u.Host
}
