// Source: abstraction/server/service_scenario/design.md (service_scenario v0.1.1)
package server

import (
	"bytes"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"testbed/internal/sampling"
)

const scheduleExample = `{"states": {"fast": {"service_us": 400000}, "down": {"stall": true}, "slow": {"service_us": 800000}},
 "schedule": [[0, "fast"], [60000, "down"], [80000, "slow"]]}`

const markovExample = `{"states": {"down": {"stall": true}, "fast": {"service_us": 400000}},
 "markov": {"initial": "fast", "step_ms": 1000, "p": {"fast": {"down": 0.6, "fast": 0.4}, "down": {"down": 0.7, "fast": 0.3}}}}`

func mustScenario(t *testing.T, js string, seed uint64) *Scenario {
	t.Helper()
	sc, err := NewScenario(js, seed)
	if err != nil {
		t.Fatalf("NewScenario: %v", err)
	}
	return sc
}

// Design: abstraction/server/service_scenario/design.md#worked-examples
func TestScenarioScheduleWorkedExample(t *testing.T) {
	sc := mustScenario(t, scheduleExample, 0)
	t0 := time.Now()
	if s := sc.StateAt(t0); s.Name != "fast" {
		t.Fatalf("not armed: state %s, want fast", s.Name)
	}
	if _, ok := sc.NextChangeAt(t0); ok {
		t.Fatal("not armed: next_change_at must be none")
	}
	sc.Arm(t0)
	rows := []struct {
		t        time.Duration
		state    string
		next     time.Duration
		nextNone bool
	}{
		{0, "fast", 60 * time.Second, false},
		{59999999999, "fast", 60 * time.Second, false},
		{60 * time.Second, "down", 80 * time.Second, false},
		{79900 * time.Millisecond, "down", 80 * time.Second, false},
		{80 * time.Second, "slow", 0, true},
		{500 * time.Second, "slow", 0, true},
	}
	for _, r := range rows {
		now := t0.Add(r.t)
		if s := sc.StateAt(now); s.Name != r.state {
			t.Errorf("t=%v: state %s, want %s", r.t, s.Name, r.state)
		}
		n, ok := sc.NextChangeAt(now)
		if ok == r.nextNone || (ok && n.Sub(t0) != r.next) {
			t.Errorf("t=%v: next_change_at (%v, %v), want %v (none: %v)", r.t, n.Sub(t0), ok, r.next, r.nextNone)
		}
	}
	if s := sc.StateAt(t0.Add(60 * time.Second)); !s.Stall || s.ServiceUs != 0 {
		t.Errorf("down must be a StalledState, got %+v", *s)
	}
	if s := sc.StateAt(t0.Add(90 * time.Second)); s.Stall || s.ServiceUs != 800000 {
		t.Errorf("slow must be 800000 µs, got %+v", *s)
	}
	want := []scenarioChange{{0, sc.states["fast"]}, {60_000_000_000, sc.states["down"]}, {80_000_000_000, sc.states["slow"]}}
	if len(sc.changes) != len(want) {
		t.Fatalf("changes %v, want %v", sc.changes, want)
	}
	for i := range want {
		if sc.changes[i] != want[i] {
			t.Errorf("change %d = %+v, want %+v", i, sc.changes[i], want[i])
		}
	}
}

// Design: abstraction/server/service_scenario/design.md#worked-examples
// Design: abstraction/server/service_scenario/design.md#ordering
func TestScenarioMarkovWorkedExample(t *testing.T) {
	if got := sampling.DeriveSeed(42, sampling.StreamServerScenario, 0); got != 7197217839792819605 {
		t.Fatalf("derive_seed(42, 7, 0) = %d, want 7197217839792819605", got)
	}
	us := []string{"0.64914772414042154", "0.58157259907852199", "0.78373825975853983",
		"0.45530670850642208", "0.68629871035240841", "0.62169536156500282"}
	rng := sampling.NewRng(7197217839792819605)
	for i, want := range us {
		w, _ := strconv.ParseFloat(want, 64)
		if u := rng.NextUnit(); u != w {
			t.Fatalf("u_%d = %.17g, want %s", i+1, u, want)
		}
	}
	sc := mustScenario(t, markovExample, 42)
	t0 := time.Now()
	sc.Arm(t0)
	steps := []string{"fast", "fast", "down", "fast", "down", "down", "down"} // s_0 .. s_6
	for k, want := range steps {
		if s := sc.StateAt(t0.Add(time.Duration(k) * time.Second)); s.Name != want {
			t.Errorf("s_%d = %s, want %s", k, s.Name, want)
		}
	}
	if s := sc.StateAt(t0.Add(2500 * time.Millisecond)); s.Name != "down" {
		t.Errorf("state at 2.5 s = %s, want down (an older t after the chain moved on)", s.Name)
	}
	if s := sc.StateAt(t0.Add(3200 * time.Millisecond)); s.Name != "fast" {
		t.Errorf("state at 3.2 s = %s, want fast", s.Name)
	}
	if n, ok := sc.NextChangeAt(t0.Add(2500 * time.Millisecond)); !ok || n.Sub(t0) != 3*time.Second {
		t.Errorf("next_change_at(2.5 s) = (%v, %v), want 3 s", n.Sub(t0), ok)
	}
	wantChanges := []struct {
		t     uint64
		state string
	}{{0, "fast"}, {2_000_000_000, "down"}, {3_000_000_000, "fast"}, {4_000_000_000, "down"}}
	if len(sc.changes) != len(wantChanges) {
		t.Fatalf("%d changes, want %d", len(sc.changes), len(wantChanges))
	}
	for i, w := range wantChanges {
		if sc.changes[i].tNs != w.t || sc.changes[i].state.Name != w.state {
			t.Errorf("change %d = (%d, %s), want (%d, %s)", i, sc.changes[i].tNs, sc.changes[i].state.Name, w.t, w.state)
		}
	}

	// Ordering: the sequence depends only on the seed and the stream, not on who asks when.
	jump := mustScenario(t, markovExample, 42)
	jump.Arm(t0)
	if s := jump.StateAt(t0.Add(6 * time.Second)); s.Name != "down" {
		t.Errorf("jumping straight to 6 s: %s, want down", s.Name)
	}
	if s := jump.StateAt(t0.Add(3 * time.Second)); s.Name != "fast" {
		t.Errorf("after jumping, state at 3 s = %s, want fast", s.Name)
	}
	other := mustScenario(t, strings.Replace(markovExample, `}}}}`, `}}}, "stream": 1}`, 1), 42)
	if other.stream != 1 {
		t.Fatalf("stream = %d, want 1", other.stream)
	}
	if other.rng.NextU64() == sampling.NewRng(7197217839792819605).NextU64() {
		t.Error("stream 1 must draw from its own stream")
	}
}

// Design: abstraction/server/service_scenario/design.md#worked-examples
func TestScenarioValidation(t *testing.T) {
	rows := []struct{ js, err string }{
		{`{"states": {}, "schedule": [[0, "a"]]}`, "states must be a non-empty object"},
		{`{"states": {"a": {"service_us": 0}}, "schedule": [[0, "a"]]}`, "state a needs service_us >= 1 or stall: true"},
		{`{"states": {"a": {"stall": true}}}`, "give exactly one of schedule and markov"},
		{`{"states": {"a": {"stall": true}}, "schedule": [[5, "a"]]}`, "bad schedule"},
		{`{"states": {"a": {"service_us": 1}}, "schedule": [[0, "a"], [0, "a"]]}`, "bad schedule"},
		{`{"states": {"a": {"service_us": 1}, "b": {"stall": true}}, "markov": {"initial": "a", "step_ms": 10, "p": {"a": {"a": 0.5, "b": 0.4}, "b": {"b": 1}}}}`, "bad markov row a"},
		{`{"states": {"a": {"service_us": 1}, "b": {"stall": true}}, "markov": {"initial": "a", "step_ms": 10, "p": {"a": {"a": 1}}}}`, "bad markov row b"},
		{`{"states": {"a": {"service_us": 1}}, "schedule": [[0, "a"]], "extra": 1}`, "unknown key extra"},
		{`[1]`, "not a JSON object"},
		{`{"states": {"a": {"service_us": 1}}, "schedule": [[0, "a"]]} x`, "not a JSON object"},
		{`{"states": {"a b": {"service_us": 1}}, "schedule": [[0, "a b"]]}`, "bad state name a b"},
		{`{"states": {"a": {"service_us": 1, "stall": true}}, "schedule": [[0, "a"]]}`, "state a needs service_us >= 1 or stall: true"},
		{`{"states": {"a": {"stall": false}}, "schedule": [[0, "a"]]}`, "state a needs service_us >= 1 or stall: true"},
		{`{"states": {"a": {"service_us": 1.5}}, "schedule": [[0, "a"]]}`, "state a needs service_us >= 1 or stall: true"},
		{`{"states": {"a": {"service_us": 1}}, "schedule": [[0, "a"]], "stream": 4294967296}`, "bad stream"},
		{`{"states": {"a": {"service_us": 1}}, "schedule": [[0, "zz"]]}`, "bad schedule"},
		{`{"states": {"a": {"service_us": 1}}, "markov": {"initial": "a", "step_ms": 0, "p": {"a": {"a": 1}}}}`, "bad markov"},
		{`{"states": {"a": {"service_us": 1}}, "markov": {"initial": "q", "step_ms": 5, "p": {"a": {"a": 1}}}}`, "bad markov"},
		{`{"states": {"a": {"service_us": 1}}, "markov": {"initial": "a", "step_ms": 5, "p": {"a": {"a": -0.5, "b": 1.5}}}}`, "bad markov row a"},
	}
	for _, r := range rows {
		_, err := NewScenario(r.js, 0)
		if err == nil || err.Error() != "service_scenario: "+r.err {
			t.Errorf("%s: error %v, want service_scenario: %s", r.js, err, r.err)
		}
	}
	// A row within MARKOV_ROW_TOLERANCE of 1 and an absorbing state are valid.
	ok := `{"states": {"a": {"service_us": 1}, "b": {"stall": true}}, "markov": {"initial": "a", "step_ms": 1, "p": {"a": {"a": 0.1, "b": 0.2, "c": 0}, "b": {"b": 1}}}}`
	if _, err := NewScenario(ok, 0); err == nil {
		t.Error("a row naming an unknown state must be refused")
	}
	ok = `{"states": {"a": {"service_us": 1}, "b": {"stall": true}}, "markov": {"initial": "a", "step_ms": 1, "p": {"a": {"a": 0.1, "b": 0.2, "a2": 0}, "b": {"b": 1}}}}`
	if _, err := NewScenario(ok, 0); err == nil {
		t.Error("a row naming an unknown state must be refused")
	}
	ok = `{"states": {"a": {"service_us": 1}, "b": {"stall": true}}, "markov": {"initial": "a", "step_ms": 1, "p": {"a": {"a": 0.1, "b": 0.9000000001}, "b": {"b": 1}}}}`
	if _, err := NewScenario(ok, 0); err != nil {
		t.Errorf("a row within tolerance: %v", err)
	}
}

// Design: abstraction/server/service_scenario/design.md#failure-and-edge-cases
func TestScenarioEdgeCases(t *testing.T) {
	sc := mustScenario(t, scheduleExample, 0)
	t0 := time.Now()
	sc.Arm(t0)
	sc.Arm(t0.Add(time.Hour)) // only the first call counts
	if s := sc.StateAt(t0.Add(-time.Second)); s.Name != "fast" {
		t.Errorf("a reading before the arm time: %s, want fast (clock 0)", s.Name)
	}
	if s := sc.StateAt(t0.Add(61 * time.Second)); s.Name != "down" {
		t.Errorf("second Arm must not move the clock: %s", s.Name)
	}
	// An absorbing Markov state never changes, whatever the draws.
	abs := mustScenario(t, `{"states": {"a": {"service_us": 7}}, "markov": {"initial": "a", "step_ms": 1, "p": {"a": {"a": 1}}}}`, 9)
	abs.Arm(t0)
	if s := abs.StateAt(t0.Add(5 * time.Second)); s.Name != "a" || len(abs.changes) != 1 || abs.k != 5000 {
		t.Errorf("absorbing: state %s, %d changes, k %d", s.Name, len(abs.changes), abs.k)
	}
	if n, ok := abs.NextChangeAt(t0.Add(5 * time.Second)); !ok || n.Sub(t0) != 5001*time.Millisecond {
		t.Errorf("markov next_change_at = (%v, %v), want 5.001 s", n.Sub(t0), ok)
	}
}

type closingBuffer struct {
	mu     sync.Mutex
	b      bytes.Buffer
	closed bool
}

func (c *closingBuffer) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.b.Write(p)
}
func (c *closingBuffer) Close() error { c.mu.Lock(); c.closed = true; c.mu.Unlock(); return nil }
func (c *closingBuffer) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.b.String()
}

// Design: abstraction/server/service_scenario/design.md#scenariolog
func TestScenarioLogRows(t *testing.T) {
	sc := mustScenario(t, `{"states": {"fast": {"service_us": 400000}, "down": {"stall": true}, "slow": {"service_us": 800000}},
		"schedule": [[0, "fast"], [40, "down"], [60, "down"], [80, "slow"]]}`, 0)
	out := &closingBuffer{}
	l := StartScenarioLogger(sc, out)
	time.Sleep(20 * time.Millisecond)
	if got := out.String(); got != "t_ns,unix_ns,state\n" {
		t.Fatalf("before the arm: %q, want the header only", got)
	}
	t0 := time.Now()
	sc.Arm(t0)
	at := uint64(t0.UnixNano())
	time.Sleep(150 * time.Millisecond)
	l.Stop()
	want := "t_ns,unix_ns,state\n" +
		"0," + strconv.FormatUint(at, 10) + ",fast\n" +
		"40000000," + strconv.FormatUint(at+40000000, 10) + ",down\n" + // the repeated entry at 60 ms writes no row
		"80000000," + strconv.FormatUint(at+80000000, 10) + ",slow\n"
	if got := out.String(); got != want {
		t.Errorf("ScenarioLog\n%s\nwant\n%s", got, want)
	}
	if !out.closed {
		t.Error("Stop must close the file")
	}

	// A MarkovSchedule is advanced by the writer itself: rows at exact step boundaries, no worker asking.
	mk := mustScenario(t, `{"states": {"down": {"stall": true}, "fast": {"service_us": 400000}},
		"markov": {"initial": "fast", "step_ms": 10, "p": {"fast": {"down": 0.6, "fast": 0.4}, "down": {"down": 0.7, "fast": 0.3}}}}`, 42)
	out2 := &closingBuffer{}
	l2 := StartScenarioLogger(mk, out2)
	t1 := time.Now()
	mk.Arm(t1)
	time.Sleep(75 * time.Millisecond)
	l2.Stop()
	at1 := uint64(t1.UnixNano())
	want2 := "t_ns,unix_ns,state\n" +
		"0," + strconv.FormatUint(at1, 10) + ",fast\n" +
		"20000000," + strconv.FormatUint(at1+20000000, 10) + ",down\n" +
		"30000000," + strconv.FormatUint(at1+30000000, 10) + ",fast\n" +
		"40000000," + strconv.FormatUint(at1+40000000, 10) + ",down\n"
	if got := out2.String(); got != want2 {
		t.Errorf("Markov ScenarioLog\n%s\nwant\n%s", got, want2)
	}
}
