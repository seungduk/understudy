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

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/seungduk/understudy/control"
	"github.com/seungduk/understudy/mcpserver"
	"github.com/seungduk/understudy/script"
	"github.com/seungduk/understudy/server"
	"github.com/seungduk/understudy/ui"
)

// MCPPath is where understudy serve answers MCP over streamable HTTP.
const MCPPath = server.ControlPrefix + "mcp"

// onOff is a feature switch of a flag.
type onOff int

const (
	on onOff = iota
	off
)

func parseOnOff(cmd, flagName, v string, stderr io.Writer) (onOff, bool) {
	switch v {
	case "on":
		return on, true
	case "off":
		return off, true
	}
	fmt.Fprintf(stderr, "understudy %s: unknown --%s %q. Fix: use on or off\n", cmd, flagName, v)
	return off, false
}

// mount puts the UI and the MCP endpoint in front of the server's handler.
// self is the server's own base URL, which the MCP tools call.
func mount(s *server.Server, uiMode, mcpMode onOff, self string) (http.Handler, error) {
	mux := http.NewServeMux()
	mux.Handle("/", s.Handler())
	if uiMode == on {
		mux.Handle("GET "+ui.Prefix, ui.Handler(ui.Prefix))
		mux.Handle("HEAD "+ui.Prefix, ui.Handler(ui.Prefix))
		mux.Handle("GET /_understudy/ui", ui.Handler(ui.Prefix))
	}
	if mcpMode == on {
		c, err := control.New(self, nil)
		if err != nil {
			return nil, err
		}
		ms := mcpserver.New(mcpserver.Options{Control: c})
		mux.Handle(MCPPath, mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return ms }, nil))
	}
	return mux, nil
}

// selfURL is a URL this process can reach its own listener at.
func selfURL(ln net.Listener) string {
	addr := ln.Addr().(*net.TCPAddr)
	host := addr.IP.String()
	if addr.IP.IsUnspecified() {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, fmt.Sprint(addr.Port))
}

func cmdServe(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", "127.0.0.1:8765", "address to listen on (port 0 picks a free port)")
	file := fs.String("script", "", "script file to load at start")
	clk := fs.String("clock", "real", "clock: real or virtual")
	uiFlag := fs.String("ui", "on", "the web UI at "+ui.Prefix+": on or off")
	mcpFlag := fs.String("mcp", "on", "MCP over streamable HTTP at "+MCPPath+": on or off")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	uiMode, ok1 := parseOnOff("serve", "ui", *uiFlag, stderr)
	mcpMode, ok2 := parseOnOff("serve", "mcp", *mcpFlag, stderr)
	if !ok1 || !ok2 {
		return 2
	}
	o, code := serverOptions("serve", *clk, *file, stderr)
	if code != 0 {
		return code
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintf(stderr, "understudy serve: cannot listen on %s: %v. Fix: pick a free address with --addr\n", *addr, err)
		return 1
	}
	s := server.New(o)
	h, err := mount(s, uiMode, mcpMode, selfURL(ln))
	if err != nil {
		fmt.Fprintf(stderr, "understudy serve: %v\n", err)
		return 1
	}
	hs := &http.Server{Handler: h}
	fmt.Fprintf(stdout, "understudy %s listening on http://%s\n", server.Version, ln.Addr())
	if uiMode == on {
		fmt.Fprintf(stdout, "  web UI: %s%s\n", selfURL(ln), ui.Prefix)
	}
	if mcpMode == on {
		fmt.Fprintf(stdout, "  MCP (streamable HTTP): %s%s\n", selfURL(ln), MCPPath)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		s.Reset() // release held requests
		_ = hs.Close()
	}()
	if err := hs.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(stderr, "understudy serve: %v\n", err)
		return 1
	}
	return 0
}

func serverOptions(cmd, clk, file string, stderr io.Writer) (server.Options, int) {
	o := server.Options{}
	switch clk {
	case "real":
	case "virtual":
		o.Clock = server.VirtualClock
	default:
		fmt.Fprintf(stderr, "understudy %s: unknown --clock %q. Fix: use real or virtual\n", cmd, clk)
		return o, 2
	}
	if file != "" {
		sc, err := script.Load(file)
		if err != nil {
			fmt.Fprintf(stderr, "understudy %s: %s: %v\n", cmd, file, err)
			return o, 1
		}
		o.Script = sc
	}
	return o, 0
}
