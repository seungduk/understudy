// Package record turns real agent sessions into understudy scripts:
// a Claude Code stream-json transcript (FromTranscript) or live traffic of any
// harness through a recording proxy (Proxy). Recorded scripts are ordinary
// scripts: agents read and edit them like hand-written ones.
//
// A recorded script replays the model's real replies turn by turn. Its
// divergence policy (Meta.Divergence) says what happens when the agent, on
// replay, sends something the recording did not have: strict refuses the
// request, lenient answers it anyway. The commands `understudy record` and
// `understudy record-proxy` are this package.
//
// # Stability
//
// understudy is at v0: until v1.0.0, a minor release may change this package's
// API, and the changelog says how. Patch releases do not.
package record
