// Package script is the understudy script format: YAML files (schema
// understudy/v1 or understudy/v2) that say, per session, what the fake model
// replies at each turn.
//
// A File is the plain data an agent writes; Compile checks it and gives a
// Script, the only form the server runs. Parse and Load read YAML (or JSON,
// which is YAML too) and refuse unknown fields with fix-it problems: a
// Problems error lists each one with its path, line, rule and fix.
//
// The engine is protocol- and harness-neutral: it reads tool results as
// core-level Results and answers with Replies; the server turns those into
// each protocol's wire format and each harness's tools.
//
// The format itself, every field and worked examples, is the guide SCRIPTS.md
// (also embedded as understudy.ScriptsGuide); SchemaJSON is its JSON Schema.
//
// # Stability
//
// understudy is at v0: until v1.0.0, a minor release may change this package's
// API, and the changelog says how. Patch releases do not.
package script
