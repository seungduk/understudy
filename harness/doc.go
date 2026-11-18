// Package harness is the agent harnesses understudy plays the model for, as
// data: for each harness, the protocols it speaks, how a request shows it is
// that harness, how each neutral action (shell, read, write, edit) becomes its
// own tool call, how its tool results show errors and exit codes, where its
// prompt names the working directory, which side requests it makes (titles,
// routing) and how to point it at understudy. The code here only reads that
// data; adding a harness is adding one profile file.
//
// The profiles are Claude Code, OpenAI's Codex CLI, opencode, pi, Gemini CLI,
// Google Antigravity's CLI, and a generic profile for any other client. Names
// lists them, Lookup finds one by name, and Detect tells which one sent a
// request. understudytest.Harness runs a harness from its profile's Launch
// data.
//
// # Stability
//
// understudy is at v0: until v1.0.0, a minor release may change this package's
// API, and the changelog says how. Patch releases do not.
package harness
