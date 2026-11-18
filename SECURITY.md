# Security policy

## Supported versions

understudy is at v0. Security fixes go into the latest release only; upgrade to it to get them.

| Version | Supported |
|---|---|
| latest 0.x release | yes |
| older releases | no |

## What understudy is, and is not

understudy is a test tool. It is meant to listen on a loopback address (`127.0.0.1`, the
default) on a developer's machine or in CI, and it never needs a real API key: the clients it
configures get dummy credentials only. Its control API, web UI and MCP endpoint have no
authentication, by design. Exposing `understudy serve` on a public network is not a supported
setup.

Reports we want: a way for understudy to leak a real credential (for example, `understudy env`,
`understudy setup` or the test helpers leaving a real key in a child's environment), a way for
the recording proxy to store credentials or forward them anywhere but the upstream given, a
path traversal or code execution through a script, a request or the web UI, or a release
artifact that does not match its checksum.

## Reporting a vulnerability

Please do not open a public issue. Report it privately through GitHub:
<https://github.com/seungduk/understudy/security/advisories/new>.

Say what you found, the version (`understudy version`), and how to reproduce it. You will get an
answer within seven days. When the fix is released, the advisory is published with credit to you,
unless you ask otherwise.
