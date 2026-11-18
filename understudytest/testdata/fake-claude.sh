#!/bin/sh
# A stand-in for the claude CLI in this package's own tests: it checks the
# environment the helper sets and prints a short stream-json transcript.
[ -n "$CLAUDE_CONFIG_DIR" ] || { echo "no config dir" >&2; exit 9; }
case "$ANTHROPIC_BASE_URL" in http://127.0.0.1:*) ;; *) echo "bad base url" >&2; exit 9;; esac
[ "$ANTHROPIC_CUSTOM_HEADERS" = "X-Understudy-Session: s1" ] || { echo "bad header" >&2; exit 9; }
echo '{"type":"system","subtype":"init","claude_code_version":"0.0.0-fake"}'
echo 'not json'
echo ''
echo '{"type":"system","subtype":"api_retry"}'
echo "{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":true,\"result\":\"args: $*\"}"
exit 3
