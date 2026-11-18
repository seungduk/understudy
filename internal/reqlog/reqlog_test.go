package reqlog

import (
	"sync"
	"testing"
)

func TestLog(t *testing.T) {
	var l Log
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := "a"
			if i%2 == 1 {
				s = "b"
			}
			seq := l.Add(Entry{Session: s, Outcome: Pending, Trace: []string{"x"}, History: [][]Result{{{Text: "t"}}}, Headers: map[string]string{"k": "v"}})
			l.Update(seq, func(e *Entry) { e.Outcome = Served })
		}(i)
	}
	wg.Wait()
	all := l.Entries(nil)
	if len(all) != 50 {
		t.Fatal(len(all))
	}
	for i, e := range all {
		if e.Seq != i+1 || e.Outcome != Served {
			t.Fatalf("%+v", e)
		}
	}
	if b := l.Entries(func(e Entry) bool { return e.Session == "b" }); len(b) != 25 {
		t.Fatal(len(b))
	}
	// Copies do not alias the log.
	all[0].Trace[0], all[0].History[0][0].Text, all[0].Headers["k"] = "y", "u", "w"
	if e := l.Entries(nil)[0]; e.Trace[0] != "x" || e.History[0][0].Text != "t" || e.Headers["k"] != "v" {
		t.Fatal("aliased")
	}
	l.Update(0, func(*Entry) { t.Fatal("called") })
	l.Update(99, func(*Entry) { t.Fatal("called") })
	l.Reset()
	if len(l.Entries(nil)) != 0 {
		t.Fatal("reset")
	}
	if e := clone(Entry{}); e.History != nil || e.Headers != nil {
		t.Fatal("nil clone")
	}
}

func TestOnChangeAndDeepCopies(t *testing.T) {
	n := 0
	l := Log{OnChange: func() { n++ }}
	seq := l.Add(Entry{Answer: &Answer{Kind: "tool_use", Calls: []Call{{Tool: "Read", Input: []byte(`{}`)}}, Faults: []string{"f"}},
		Held: &Hold{Gate: "g"}, Expected: []string{"e"}, Actual: []string{"a"}})
	l.Update(seq, func(e *Entry) {})
	l.Update(7, func(e *Entry) {}) // unknown: no change
	l.Reset()
	if n != 3 {
		t.Fatalf("OnChange called %d times, want 3", n)
	}
	seq = l.Add(Entry{Answer: &Answer{Calls: []Call{{Tool: "Read"}}, Faults: []string{"f"}}, Held: &Hold{Gate: "g"}, Expected: []string{"e"}, Actual: []string{"a"}})
	c := l.Entries(nil)[0]
	c.Answer.Calls[0].Tool, c.Answer.Faults[0], c.Answer.Kind, c.Held.Gate, c.Expected[0], c.Actual[0] = "x", "x", "x", "x", "x", "x"
	e := l.Entries(nil)[0]
	if e.Answer.Calls[0].Tool != "Read" || e.Answer.Faults[0] != "f" || e.Answer.Kind != "" || e.Held.Gate != "g" || e.Expected[0] != "e" || e.Actual[0] != "a" || e.Seq != seq {
		t.Fatalf("aliased: %+v", e)
	}
	var quiet Log // no OnChange: nothing to call
	quiet.Add(Entry{})
}
