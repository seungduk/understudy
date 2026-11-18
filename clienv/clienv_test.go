package clienv

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestEnv(t *testing.T) {
	e := strings.Join(Env("http://h:1/", "t1/writer", APIKey), "\n")
	for _, want := range []string{"ANTHROPIC_BASE_URL=http://h:1\n", "ANTHROPIC_API_KEY=" + DummyAPIKey, "ANTHROPIC_CUSTOM_HEADERS=X-Understudy-Session: t1/writer", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"} {
		if !strings.Contains(e, want) {
			t.Errorf("missing %q", want)
		}
	}
	g := strings.Join(Env("http://h:1", "", Gateway), "\n")
	if !strings.Contains(g, "ANTHROPIC_AUTH_TOKEN="+DummyGatewayToken) || strings.Contains(g, "API_KEY") || strings.Contains(g, "CUSTOM_HEADERS") {
		t.Error(g)
	}
	parts := strings.Split(DummyGatewayToken, ".")
	hdr, _ := base64.RawURLEncoding.DecodeString(parts[0])
	pay, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var p map[string]any
	if json.Unmarshal(pay, &p) != nil || p["aud"] != "claude-gateway" || string(hdr) != `{"alg":"none","typ":"JWT"}` {
		t.Errorf("%s %s", hdr, pay)
	}
}

func TestMerge(t *testing.T) {
	got := Merge([]string{"PATH=/bin", "ANTHROPIC_API_KEY=real", "CLAUDE_CODE_OAUTH_TOKEN=x", "DISABLE_TELEMETRY=0", "HOME=/h"}, []string{"DISABLE_TELEMETRY=1", "A=b"})
	if strings.Join(got, ",") != "PATH=/bin,HOME=/h,DISABLE_TELEMETRY=1,A=b" {
		t.Error(got)
	}
}
