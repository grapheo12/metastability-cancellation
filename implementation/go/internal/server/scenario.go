// Source: abstraction/server/service_scenario/design.md (service_scenario v0.1.1)
package server

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/bits"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"testbed/internal/sampling"
)

// API: abstraction/server/service_scenario/api.md#constants
const (
	// MarkovRowTolerance is MARKOV_ROW_TOLERANCE: a MarkovSchedule row sums to 1 within this.
	MarkovRowTolerance = 1e-9
	// maxScenarioMs is MAX_MS = floor((2^64 - 1) / 1_000_000), the largest ms that fits in u64 ns.
	maxScenarioMs = math.MaxUint64 / 1_000_000
)

var scenarioStateName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ScenarioState is one state of a ServiceScenario: a ConstantServiceTime (ServiceUs) or a StalledState.
//
// Design: abstraction/server/service_scenario/design.md#stalledstate
type ScenarioState struct {
	Name      string
	ServiceUs uint64 // the ConstantServiceTime of a request popped in this state; 0 for a StalledState
	Stall     bool
}

// scenarioChange is one (ScenarioClock ns, state) pair: a schedule entry or a change of state.
type scenarioChange struct {
	tNs   uint64
	state *ScenarioState
}

// markovTarget is one entry of a MarkovSchedule row; a row's targets are in bytewise name order.
type markovTarget struct {
	to string
	p  float64
}

// Scenario is the ServiceScenario: its states, a ScenarioSchedule or a MarkovSchedule, the ScenarioClock and the
// list of changes the ScenarioLog writes. Everything below mu is guarded by it (one lock per scenario); the
// fields above it never change after NewScenario.
//
// Design: abstraction/server/service_scenario/design.md#servicescenario
// Design: abstraction/server/service_scenario/design.md#concurrency
type Scenario struct {
	states   map[string]*ScenarioState
	stream   uint32
	schedule []scenarioChange // ScenarioSchedule (at_ns, state); nil for a MarkovSchedule
	stepNs   uint64           // MarkovSchedule
	rows     map[string][]markovTarget

	armedCh chan struct{} // closed by Arm: the ScenarioLog writer waits on it

	mu            sync.Mutex
	armed         bool
	armedAt       time.Time // monotonic reading: the ScenarioClock's origin
	armedAtUnixNs uint64    // wall-clock reading of the same instant, for the ScenarioLog
	rng           *sampling.Rng
	k             uint64         // the MarkovSchedule's newest step
	cur           *ScenarioState // its state s_k
	changes       []scenarioChange
}

func scenarioError(msg string) error { return errors.New("service_scenario: " + msg) }

// jsonUint reads a JSON integer in 0 ..= max (digits only: no fraction, no exponent).
func jsonUint(v interface{}, max uint64) (uint64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	u, err := strconv.ParseUint(string(n), 10, 64)
	if err != nil || u > max {
		return 0, false
	}
	return u, true
}

func sortedKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys) // bytewise
	return keys
}

// NewScenario is scenario_new: it parses and validates service_scenario. Errors are checked in the design's order;
// keys of one object are checked in bytewise order, so the first error reported never depends on the JSON's key
// order.
//
// Design: abstraction/server/service_scenario/design.md#servicescenario
func NewScenario(js string, seed uint64) (*Scenario, error) {
	dec := json.NewDecoder(strings.NewReader(js))
	dec.UseNumber()
	var raw interface{}
	if err := dec.Decode(&raw); err != nil {
		return nil, scenarioError("not a JSON object")
	}
	if _, err := dec.Token(); err != io.EOF { // nothing may follow the object
		return nil, scenarioError("not a JSON object")
	}
	v, ok := raw.(map[string]interface{})
	if !ok {
		return nil, scenarioError("not a JSON object")
	}
	for _, key := range sortedKeys(v) {
		switch key {
		case "states", "schedule", "markov", "stream":
		default:
			return nil, scenarioError("unknown key " + key)
		}
	}
	statesRaw, ok := v["states"].(map[string]interface{})
	if !ok || len(statesRaw) == 0 {
		return nil, scenarioError("states must be a non-empty object")
	}
	states := map[string]*ScenarioState{}
	for _, name := range sortedKeys(statesRaw) {
		if !scenarioStateName.MatchString(name) {
			return nil, scenarioError("bad state name " + name)
		}
		bad := scenarioError("state " + name + " needs service_us >= 1 or stall: true")
		s, ok := statesRaw[name].(map[string]interface{})
		if !ok || len(s) != 1 {
			return nil, bad
		}
		if n, has := s["service_us"]; has {
			us, ok := jsonUint(n, math.MaxUint64)
			if !ok || us < 1 {
				return nil, bad
			}
			states[name] = &ScenarioState{Name: name, ServiceUs: us}
		} else if st, has := s["stall"]; has && st == true {
			states[name] = &ScenarioState{Name: name, Stall: true}
		} else {
			return nil, bad
		}
	}
	_, hasSchedule := v["schedule"]
	_, hasMarkov := v["markov"]
	if hasSchedule == hasMarkov {
		return nil, scenarioError("give exactly one of schedule and markov")
	}
	var stream uint64
	if s, has := v["stream"]; has {
		if stream, ok = jsonUint(s, math.MaxUint32); !ok {
			return nil, scenarioError("bad stream")
		}
	}
	sc := &Scenario{states: states, stream: uint32(stream), armedCh: make(chan struct{})}
	if hasSchedule {
		// Design: abstraction/server/service_scenario/design.md#scenarioschedule
		bad := scenarioError("bad schedule")
		arr, ok := v["schedule"].([]interface{})
		if !ok || len(arr) == 0 {
			return nil, bad
		}
		for i, e := range arr {
			pair, ok := e.([]interface{})
			if !ok || len(pair) != 2 {
				return nil, bad
			}
			atMs, ok := jsonUint(pair[0], maxScenarioMs)
			if !ok {
				return nil, bad
			}
			name, ok := pair[1].(string)
			st := states[name]
			if !ok || st == nil {
				return nil, bad
			}
			atNs := atMs * 1_000_000
			if (i == 0 && atNs != 0) || (i > 0 && atNs <= sc.schedule[i-1].tNs) { // starts at 0, strictly increasing
				return nil, bad
			}
			sc.schedule = append(sc.schedule, scenarioChange{atNs, st})
			if i == 0 || st != sc.schedule[i-1].state { // the first entry, then every change of state
				sc.changes = append(sc.changes, scenarioChange{atNs, st})
			}
		}
		return sc, nil
	}
	// Design: abstraction/server/service_scenario/design.md#markovschedule
	bad := scenarioError("bad markov")
	m, ok := v["markov"].(map[string]interface{})
	if !ok || len(m) != 3 {
		return nil, bad
	}
	initial, ok1 := m["initial"].(string)
	stepMs, ok2 := jsonUint(m["step_ms"], maxScenarioMs)
	p, ok3 := m["p"].(map[string]interface{})
	if !ok1 || !ok2 || !ok3 || states[initial] == nil || stepMs < 1 {
		return nil, bad
	}
	froms := map[string]interface{}{} // every row named in p and every state: each needs exactly one valid row
	for k := range p {
		froms[k] = nil
	}
	for k := range states {
		froms[k] = nil
	}
	sc.rows = map[string][]markovTarget{}
	for _, from := range sortedKeys(froms) {
		badRow := scenarioError("bad markov row " + from)
		row, ok := p[from].(map[string]interface{})
		if states[from] == nil || !ok {
			return nil, badRow
		}
		var targets []markovTarget
		sum := 0.0
		for _, to := range sortedKeys(row) { // bytewise name order
			n, isNum := row[to].(json.Number)
			if states[to] == nil || !isNum {
				return nil, badRow
			}
			prob, err := strconv.ParseFloat(string(n), 64)
			if err != nil || !(prob >= 0) || math.IsInf(prob, 0) {
				return nil, badRow
			}
			sum += prob // binary64, left to right
			targets = append(targets, markovTarget{to, prob})
		}
		if !(math.Abs(sum-1) <= MarkovRowTolerance) {
			return nil, badRow
		}
		sc.rows[from] = targets
	}
	sc.stepNs = stepMs * 1_000_000
	sc.rng = sampling.NewRng(sampling.DeriveSeed(seed, sampling.StreamServerScenario, sc.stream))
	sc.cur = states[initial]
	sc.changes = []scenarioChange{{0, sc.cur}}
	return sc, nil
}

// Arm is scenario_arm: it starts the ScenarioClock at armedAt (only the first call counts). armedAt is one
// time.Now(), whose monotonic reading is the clock's origin and whose wall reading is the ScenarioLog's arm time.
//
// Design: abstraction/server/service_scenario/design.md#scenarioclock
func (sc *Scenario) Arm(armedAt time.Time) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.armed {
		return
	}
	sc.armed = true
	sc.armedAt = armedAt
	sc.armedAtUnixNs = uint64(armedAt.UnixNano())
	close(sc.armedCh) // notify the ScenarioLog writer
}

// clockLocked is clock: ScenarioClock ns at now, 0 before the arm or for a reading older than it. Called with mu
// held.
//
// Design: abstraction/server/service_scenario/design.md#scenarioclock
func (sc *Scenario) clockLocked(now time.Time) uint64 {
	if !sc.armed || !now.After(sc.armedAt) {
		return 0
	}
	return uint64(now.Sub(sc.armedAt)) // monotonic difference
}

// lastAtOrBefore is the state of the last entry of list with tNs <= t (list[0].tNs == 0).
func lastAtOrBefore(list []scenarioChange, t uint64) *ScenarioState {
	i := sort.Search(len(list), func(i int) bool { return list[i].tNs > t })
	return list[i-1].state
}

// scheduleNext is schedule_next: the at_ns of the first entry after t, or none.
//
// Design: abstraction/server/service_scenario/design.md#scenarioschedule
func (sc *Scenario) scheduleNext(t uint64) (uint64, bool) {
	j := sort.Search(len(sc.schedule), func(i int) bool { return sc.schedule[i].tNs > t })
	if j == len(sc.schedule) {
		return 0, false // after the last entry: that state for good
	}
	return sc.schedule[j].tNs, true
}

// markovAdvanceLocked is markov_advance: one NextUnit per step, in step order, whoever asks. Called with mu held.
//
// Design: abstraction/server/service_scenario/design.md#markovschedule
// Design: abstraction/server/service_scenario/design.md#ordering
func (sc *Scenario) markovAdvanceLocked(k uint64) {
	for sc.k < k {
		u := sc.rng.NextUnit()
		row := sc.rows[sc.cur.Name]
		next := ""
		c := 0.0
		for _, tg := range row {
			c = c + tg.p // binary64, left to right
			if u < c {
				next = tg.to
				break
			}
		}
		if next == "" { // u >= the row's total, only by rounding: the last target with p > 0
			for i := len(row) - 1; i >= 0; i-- {
				if row[i].p > 0 {
					next = row[i].to
					break
				}
			}
		}
		sc.k++
		ns := sc.states[next]
		if ns != sc.cur {
			sc.changes = append(sc.changes, scenarioChange{sc.k * sc.stepNs, ns})
		}
		sc.cur = ns
	}
}

// markovNext is markov_next: the next step boundary after t, or none if it does not fit in an int64 of ns (about
// 292 years: no run can tell that apart from a change that never comes).
//
// Design: abstraction/server/service_scenario/design.md#markovschedule
func (sc *Scenario) markovNext(t uint64) (uint64, bool) {
	hi, lo := bits.Mul64(t/sc.stepNs+1, sc.stepNs)
	if hi != 0 {
		return 0, false
	}
	return lo, true
}

// StateAt is state_at: the ScenarioState at now (the first state before the clock starts). Safe from any goroutine.
//
// Design: abstraction/server/service_scenario/design.md#state_at-and-next_change_at
func (sc *Scenario) StateAt(now time.Time) *ScenarioState {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	t := sc.clockLocked(now)
	if sc.schedule != nil {
		return lastAtOrBefore(sc.schedule, t)
	}
	if k := t / sc.stepNs; k > sc.k {
		sc.markovAdvanceLocked(k)
	}
	return lastAtOrBefore(sc.changes, t) // also answers a t older than the chain's newest step
}

// NextChangeAt is next_change_at: the earliest time after now at which the state can change, or false when no
// change can come (after the last schedule entry, or before the clock starts). A change further away than
// time.Duration can hold (about 292 years) counts as none.
//
// Design: abstraction/server/service_scenario/design.md#state_at-and-next_change_at
func (sc *Scenario) NextChangeAt(now time.Time) (time.Time, bool) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if !sc.armed {
		return time.Time{}, false // the clock has not started
	}
	t := sc.clockLocked(now)
	var n uint64
	var ok bool
	if sc.schedule != nil {
		n, ok = sc.scheduleNext(t)
	} else {
		n, ok = sc.markovNext(t)
	}
	if !ok || n > math.MaxInt64 {
		return time.Time{}, false
	}
	return sc.armedAt.Add(time.Duration(n)), true
}

// pendingRows returns the changes from index written on whose time has come (tNs <= the clock now), advancing a
// MarkovSchedule to now first, and the wall-clock arm time.
//
// Design: abstraction/server/service_scenario/design.md#scenariolog
func (sc *Scenario) pendingRows(now time.Time, written int) ([]scenarioChange, uint64) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	t := sc.clockLocked(now)
	if sc.schedule == nil {
		if k := t / sc.stepNs; k > sc.k {
			sc.markovAdvanceLocked(k)
		}
	}
	var rows []scenarioChange
	for _, c := range sc.changes[written:] {
		if c.tNs > t {
			break
		}
		rows = append(rows, c)
	}
	return rows, sc.armedAtUnixNs
}

// ScenarioLogger is the ScenarioLog writer, a goroutine started by StartScenarioLogger and ended by Stop.
//
// Design: abstraction/server/service_scenario/design.md#scenariolog
type ScenarioLogger struct {
	stop chan struct{}
	done chan struct{}
}

// StartScenarioLogger writes the ScenarioLog header to out at once, then one row for the first state when the clock
// starts and one per change of state, each at its exact ScenarioClock time. It closes out when it returns.
//
// Design: abstraction/server/service_scenario/design.md#scenariolog
func StartScenarioLogger(sc *Scenario, out io.WriteCloser) *ScenarioLogger {
	l := &ScenarioLogger{stop: make(chan struct{}), done: make(chan struct{})}
	go l.run(sc, out)
	return l
}

// Stop asks the writer to stop and waits until it has returned and closed its file.
func (l *ScenarioLogger) Stop() {
	close(l.stop)
	<-l.done
}

// Design: abstraction/server/service_scenario/design.md#scenariolog
func (l *ScenarioLogger) run(sc *Scenario, out io.WriteCloser) {
	defer close(l.done)
	w := bufio.NewWriter(out)
	finish := func(err error) { // a write error stops the log; the server keeps serving
		if err == nil {
			err = w.Flush()
		}
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "scenario log:", err)
		}
	}
	if _, err := w.WriteString("t_ns,unix_ns,state\n"); err != nil {
		finish(err)
		return
	}
	if err := w.Flush(); err != nil {
		finish(err)
		return
	}
	select {
	case <-sc.armedCh:
	case <-l.stop:
		finish(nil)
		return
	}
	written := 0
	for {
		rows, at := sc.pendingRows(time.Now(), written)
		written += len(rows)
		for _, r := range rows {
			if _, err := fmt.Fprintf(w, "%d,%d,%s\n", r.tNs, at+r.tNs, r.state.Name); err != nil {
				finish(err)
				return
			}
		}
		if err := w.Flush(); err != nil {
			finish(err)
			return
		}
		n, ok := sc.NextChangeAt(time.Now())
		if !ok {
			<-l.stop // no change can come
			finish(nil)
			return
		}
		timer := time.NewTimer(time.Until(n))
		select {
		case <-timer.C:
		case <-l.stop:
			timer.Stop()
			finish(nil)
			return
		}
	}
}
