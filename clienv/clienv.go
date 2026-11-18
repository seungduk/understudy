package clienv

import (
	"strings"
)

// Auth is how the CLI authenticates to the fake.
type Auth int

const (
	// APIKey sends a dummy x-api-key, as an API-billed account does. Claude Code
	// then treats usage-limit 429s as plain rate limits ("API Error: Request rejected (429)").
	APIKey Auth = iota
	// Gateway sends a dummy, unsigned bearer token whose audience is claude-gateway.
	// Claude Code then reads the unified rate-limit headers as it does for
	// subscription accounts and prints "You've hit your weekly limit · resets ...".
	// Measured with Claude Code 2.1.325; see docs/limits.md.
	Gateway
)

// DummyAPIKey is the API key value sent in APIKey mode. It is not a credential.
const DummyAPIKey = "understudy-dummy-key"

// DummyGatewayToken is an unsigned JWT ({"alg":"none"}) with payload
// {"aud":"claude-gateway","exp":4102444800}. It is not a credential: nothing
// can verify it, and it means something only to a fake server.
const DummyGatewayToken = "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0.eyJhdWQiOiJjbGF1ZGUtZ2F0ZXdheSIsImV4cCI6NDEwMjQ0NDgwMH0.understudy"

// Cleared are the variables that would let the CLI use a real account; Env's
// caller must remove them from the inherited environment.
var Cleared = []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_BASE_URL",
	"ANTHROPIC_CUSTOM_HEADERS", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "ANTHROPIC_UNIX_SOCKET"}

// Env is KEY=VALUE pairs that point Claude Code at baseURL. A non-empty session
// is sent as the X-Understudy-Session header on every request.
func Env(baseURL, session string, auth Auth) []string {
	env := []string{
		"ANTHROPIC_BASE_URL=" + strings.TrimSuffix(baseURL, "/"),
		"DISABLE_AUTOUPDATER=1",
		"DISABLE_TELEMETRY=1",
		"DISABLE_ERROR_REPORTING=1",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
	}
	if auth == Gateway {
		env = append(env, "ANTHROPIC_AUTH_TOKEN="+DummyGatewayToken)
	} else {
		env = append(env, "ANTHROPIC_API_KEY="+DummyAPIKey)
	}
	if session != "" {
		env = append(env, "ANTHROPIC_CUSTOM_HEADERS=X-Understudy-Session: "+session)
	}
	return env
}

// Merge is base without the Cleared variables (and without any key that env
// sets), followed by env.
func Merge(base, env []string) []string {
	drop := map[string]bool{}
	for _, k := range Cleared {
		drop[k] = true
	}
	for _, kv := range env {
		drop[strings.SplitN(kv, "=", 2)[0]] = true
	}
	out := make([]string, 0, len(base)+len(env))
	for _, kv := range base {
		if !drop[strings.SplitN(kv, "=", 2)[0]] {
			out = append(out, kv)
		}
	}
	return append(out, env...)
}
