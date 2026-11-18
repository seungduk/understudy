// Package sse holds the server-sent-event framing and text chunking every
// streamed protocol shares. It knows nothing about any protocol's events.
package sse

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// Named is a frame with an event name: "event: <name>\ndata: <data>\n\n".
func Named(name string, data []byte) []byte {
	b := make([]byte, 0, len(name)+len(data)+16)
	b = append(b, "event: "...)
	b = append(b, name...)
	b = append(b, "\ndata: "...)
	b = append(b, data...)
	return append(b, "\n\n"...)
}

// Data is a frame without an event name: "data: <data>\n\n".
func Data(data []byte) []byte {
	b := make([]byte, 0, len(data)+8)
	b = append(b, "data: "...)
	b = append(b, data...)
	return append(b, "\n\n"...)
}

// JSON marshals v, which must marshal (the codecs only pass maps and structs of plain values).
func JSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// Frame is one parsed SSE frame.
type Frame struct {
	Event string
	Data  string
}

// Parse splits a stream into frames. Lines other than event: and data: are
// ignored; several data: lines of one frame are joined by "\n".
func Parse(raw []byte) []Frame {
	var out []Frame
	var cur Frame
	has := false
	flush := func() {
		if has {
			out = append(out, cur)
		}
		cur, has = Frame{}, false
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "event:"):
			cur.Event, has = strings.TrimSpace(line[6:]), true
		case strings.HasPrefix(line, "data:"):
			d := strings.TrimPrefix(line[5:], " ")
			if cur.Data != "" {
				cur.Data += "\n"
			}
			cur.Data += d
			has = true
		}
	}
	flush()
	return out
}

// DataLines is the data of every data: line, one per line (recorders read
// JSON-per-line streams this way, which also copes with frames that lack
// their blank separator line).
func DataLines(raw []byte) []string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "data:") {
			out = append(out, strings.TrimPrefix(line[5:], " "))
		}
	}
	return out
}

// Chunk sizes: text deltas carry up to TextChunk bytes, ending on a word
// boundary where one exists; JSON fragments carry up to JSONChunk bytes.
const (
	TextChunk = 24
	JSONChunk = 16
)

// SplitText cuts s into pieces of at most max bytes (a longer word is cut on a
// rune boundary), preferring to end a piece after a space. Joining the pieces gives s.
func SplitText(s string, max int) []string {
	var out []string
	for len(s) > 0 {
		if len(s) <= max {
			out = append(out, s)
			break
		}
		cut := strings.LastIndexByte(s[:max], ' ') + 1
		if cut <= 0 {
			cut = runeCut(s, max)
		}
		out = append(out, s[:cut])
		s = s[cut:]
	}
	return out
}

// SplitBytes cuts s into pieces of at most max bytes on rune boundaries
// (a piece is longer only when one rune is longer than max).
func SplitBytes(s string, max int) []string {
	var out []string
	for len(s) > 0 {
		cut := runeCut(s, max)
		out = append(out, s[:cut])
		s = s[cut:]
	}
	return out
}

func runeCut(s string, max int) int {
	if len(s) <= max {
		return len(s)
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	if cut == 0 {
		_, n := utf8.DecodeRuneInString(s)
		return n
	}
	return cut
}
