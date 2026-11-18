// Package clienv is the environment that points the real Claude Code CLI at an
// understudy server, with dummy credentials only.
//
// Env gives the variables to set (the base URL, the session header, a dummy key
// or gateway token, and switches that stop the CLI's own network traffic), and
// Merge applies them over an inherited environment after removing every
// variable in Cleared, so a child process can never reach a real account.
//
// The understudy command prints the same variables with `understudy env`; the
// understudytest package uses Env when it runs the CLI in a test.
//
// # Stability
//
// understudy is at v0: until v1.0.0, a minor release may change this package's
// API, and the changelog says how. Patch releases do not.
package clienv
