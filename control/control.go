// Package control is a client of a running understudy server's control API
// (the endpoints under /_understudy/). Every error says what to do next.
package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/seungduk/understudy/script"
	"github.com/seungduk/understudy/server"
)

// Client talks to one server. Safe for concurrent use.
type Client struct {
	base string
	hc   *http.Client
}

// New makes a client of the server at base, such as http://127.0.0.1:8765.
// A nil hc uses a client with a 30 s timeout.
func New(base string, hc *http.Client) (*Client, error) {
	u, err := url.Parse(strings.TrimSuffix(base, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("%q is not a server URL. Fix: give the base URL only, such as http://127.0.0.1:8765", base)
	}
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{base: u.Scheme + "://" + u.Host, hc: hc}, nil
}

// URL is the server's base URL.
func (c *Client) URL() string { return c.base }

// Error is a refusal of the control API, or a server that cannot be reached.
type Error struct {
	Status   int              `json:"status,omitempty"` // 0 when the server could not be reached
	Message  string           `json:"error"`
	Fix      string           `json:"fix"`
	Problems []script.Problem `json:"problems,omitempty"`
}

func (e *Error) Error() string {
	s := e.Message
	if len(e.Problems) > 0 {
		s += ": " + script.Problems(e.Problems).Error()
	}
	return s + ". Fix: " + e.Fix
}

func (c *Client) do(ctx context.Context, method, path string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.base+server.ControlPrefix+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return &Error{Message: fmt.Sprintf("cannot reach the understudy server at %s: %v", c.base, err),
			Fix: "start it with 'understudy serve' (or let 'understudy mcp' start one), or give the right --url"}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, server.MaxBody))
	if err != nil {
		return &Error{Status: resp.StatusCode, Message: "the response broke off: " + err.Error(), Fix: "try again"}
	}
	if resp.StatusCode >= 300 {
		var e Error
		if json.Unmarshal(data, &e) != nil || e.Message == "" {
			return &Error{Status: resp.StatusCode, Message: fmt.Sprintf("%s %s answered %d: %s", method, path, resp.StatusCode, bytes.TrimSpace(data)),
				Fix: "check that " + c.base + " is an understudy server"}
		}
		e.Status = resp.StatusCode
		if e.Fix == "" {
			e.Fix = "fix the problems listed and send the script again"
		}
		return &e
	}
	switch o := out.(type) {
	case nil:
		return nil
	case *[]byte:
		*o = data
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return &Error{Status: resp.StatusCode, Message: fmt.Sprintf("%s %s did not answer JSON: %v", method, path, err),
			Fix: "check that " + c.base + " is an understudy server of this version"}
	}
	return nil
}

// Health is GET health.
type Health struct {
	OK           bool   `json:"ok"`
	Version      string `json:"version"`
	ScriptLoaded bool   `json:"script_loaded"`
}

// Health checks that the server answers.
func (c *Client) Health(ctx context.Context) (Health, error) {
	var h Health
	err := c.do(ctx, "GET", "health", nil, &h)
	return h, err
}

// Load loads a script's text and gives its session keys. A script that does not
// check gives an *Error with its Problems.
func (c *Client) Load(ctx context.Context, src []byte) ([]string, error) {
	var out struct {
		Sessions []string `json:"sessions"`
	}
	if err := c.do(ctx, "PUT", "script", src, &out); err != nil {
		return nil, err
	}
	return out.Sessions, nil
}

// ErrNoScript is returned by Script when no script is loaded.
var ErrNoScript = errors.New("no script is loaded")

// Script is the loaded script's text.
func (c *Client) Script(ctx context.Context) ([]byte, error) {
	var b []byte
	err := c.do(ctx, "GET", "script", nil, &b)
	var e *Error
	if errors.As(err, &e) && e.Status == http.StatusNotFound {
		return nil, ErrNoScript
	}
	return b, err
}

// Requests is the request log (one session's when session is not "").
func (c *Client) Requests(ctx context.Context, session string) ([]server.Entry, error) {
	path := "requests"
	if session != "" {
		path += "?session=" + url.QueryEscape(session)
	}
	var out struct {
		Requests []server.Entry `json:"requests"`
	}
	err := c.do(ctx, "GET", path, nil, &out)
	return out.Requests, err
}

// Sessions is every session with its state.
func (c *Client) Sessions(ctx context.Context) ([]server.SessionStatus, error) {
	var out struct {
		Sessions []server.SessionStatus `json:"sessions"`
	}
	err := c.do(ctx, "GET", "sessions", nil, &out)
	return out.Sessions, err
}

// Gates is every gate seen.
func (c *Client) Gates(ctx context.Context) ([]server.GateStatus, error) {
	var out struct {
		Gates []server.GateStatus `json:"gates"`
	}
	err := c.do(ctx, "GET", "gates", nil, &out)
	return out.Gates, err
}

// Release opens a gate and says how many requests were waiting on it.
func (c *Client) Release(ctx context.Context, gate string) (int, error) {
	if !script.ValidGateName(gate) {
		return 0, &Error{Status: 400, Message: fmt.Sprintf("%q is not a gate name", gate), Fix: "use the gate name from the script's stall: {gate: NAME} (1-64 lower-case letters, digits, - and _)"}
	}
	var out struct {
		Waiting int `json:"waiting"`
	}
	err := c.do(ctx, "POST", "gates/"+gate+"/release", nil, &out)
	return out.Waiting, err
}

// Reset forgets the log, the attempt counts and the gates; the script stays.
func (c *Client) Reset(ctx context.Context) error {
	return c.do(ctx, "POST", "reset", nil, nil)
}
