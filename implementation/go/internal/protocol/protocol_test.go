// Source: abstraction/protocol/design.md (protocol v0.4.0)
package protocol

import (
	"strings"
	"testing"
)

// Design: abstraction/protocol/design.md#requestid
func TestRequestIDWorkedExamples(t *testing.T) {
	cases := []struct {
		clientID, seq uint32
		id            uint64
	}{
		{0, 0, 0},
		{0, 7, 7},
		{3, 1, 12884901889},
		{4294967295, 4294967295, 18446744073709551615},
	}
	for _, c := range cases {
		if got := MakeRequestID(c.seq, c.clientID); got != c.id {
			t.Errorf("MakeRequestID(%d,%d)=%d want %d", c.seq, c.clientID, got, c.id)
		}
		seq, cid := SplitRequestID(c.id)
		if seq != c.seq || cid != c.clientID {
			t.Errorf("SplitRequestID(%d)=(%d,%d)", c.id, seq, cid)
		}
	}
}

// Design: abstraction/protocol/design.md#paddedjsonbody
func TestBuildBodyWorkedExamples(t *testing.T) {
	cases := []struct {
		id    uint64
		total int
	}{{0, 128}, {12884901889, 128}, {18446744073709551615, 42}}
	for _, c := range cases {
		b := BuildBody(c.id, c.total)
		if len(b) != c.total {
			t.Fatalf("len=%d want %d", len(b), c.total)
		}
		got, err := ParseBody(b)
		if err != nil || got != c.id {
			t.Fatalf("round trip: %d %v", got, err)
		}
	}
	if string(BuildBody(0, 25)) != `{"id":"0","payload":"xx"}` {
		t.Fatalf("unexpected serialization %s", BuildBody(0, 25))
	}
}

// Design: abstraction/protocol/design.md#cancelbody
func TestBuildCancelBody(t *testing.T) {
	b := BuildCancelBody(12884901889)
	if string(b) != `{"id":"12884901889"}` || len(b) != 20 {
		t.Fatalf("got %s", b)
	}
}

// Design: abstraction/protocol/design.md#minimum-body-size
func TestValidateBodySize(t *testing.T) {
	if ValidateBodySize(41) == nil || ValidateBodySize(42) != nil {
		t.Fatal("minimum body size must be 42")
	}
}

// Design: abstraction/protocol/design.md#parsing
func TestParseBodyRejects(t *testing.T) {
	bad := []string{
		``, `[]`, `null`, `{}`, `{"id":5}`, `{"id":null}`, `{"id":""}`, `{"ID":"5"}`,
		`{"id":"-5"}`, `{"id":"5a"}`, `{"id":"18446744073709551616"}`, `{"id":"123456789012345678901"}`,
	}
	for _, s := range bad {
		if _, err := ParseBody([]byte(s)); err == nil {
			t.Errorf("ParseBody(%q) accepted", s)
		}
	}
	if id, err := ParseBody([]byte(`{"payload":"x","id":"42","extra":[1]}`)); err != nil || id != 42 {
		t.Errorf("lenient parse failed: %d %v", id, err)
	}
}

// Design: abstraction/protocol/design.md#cancelreplybody
func TestCancelReplyWorkedExamples(t *testing.T) {
	cases := []struct {
		finished uint64
		ok       bool
		body     string
		n        int
	}{
		{0, false, `{"finished":null}`, 17},
		{0, true, `{"finished":"0"}`, 16},
		{12884901893, true, `{"finished":"12884901893"}`, 26},
		{^uint64(0), true, `{"finished":"18446744073709551615"}`, 35},
	}
	for _, c := range cases {
		b := BuildCancelReply(c.finished, c.ok)
		if string(b) != c.body || len(b) != c.n {
			t.Errorf("BuildCancelReply(%d, %v) = %s (%d bytes)", c.finished, c.ok, b, len(b))
		}
		f, ok, err := ParseCancelReply(b)
		if err != nil || ok != c.ok || f != c.finished {
			t.Errorf("ParseCancelReply(%s) = %d %v %v", b, f, ok, err)
		}
	}
}

// Design: abstraction/protocol/design.md#cancelreplybody
func TestParseCancelReplyTable(t *testing.T) {
	if f, ok, err := ParseCancelReply([]byte(`{"finished":"42"}`)); err != nil || !ok || f != 42 {
		t.Errorf("42: %d %v %v", f, ok, err)
	}
	if _, ok, err := ParseCancelReply([]byte(`{ "finished" : null }`)); err != nil || ok {
		t.Errorf("null: %v %v", ok, err)
	}
	for _, s := range []string{`{"finished":42}`, `{}`, ``, `null`, `{"finished":""}`, `{"finished":"4x"}`} {
		if _, _, err := ParseCancelReply([]byte(s)); err == nil {
			t.Errorf("ParseCancelReply(%q) accepted", s)
		}
	}
}

// Design: abstraction/protocol/design.md#pingrequestbody
func TestBuildRequestBodyWorkedExamples(t *testing.T) {
	const dl = 1794533185297574000
	for _, tc := range []struct {
		id, deadline uint64
		total, k     int
	}{{0, dl, 128, 72}, {12884901889, dl, 128, 62}, {18446744073709551615, 18446744073709551615, 76, 0}} {
		b := BuildRequestBody(tc.id, tc.deadline, tc.total)
		if len(b) != tc.total || strings.Count(string(b), "x") != tc.k {
			t.Fatalf("id %d: len %d, k %d: %s", tc.id, len(b), strings.Count(string(b), "x"), b)
		}
		if id, err := ParseBody(b); err != nil || id != tc.id {
			t.Fatalf("id round trip: %d %v", id, err)
		}
		if d, ok := ParseDeadline(b); !ok || d != tc.deadline {
			t.Fatalf("deadline round trip: %d %v", d, ok)
		}
	}
	if !strings.HasPrefix(string(BuildRequestBody(7, 1000, 76)), `{"id":"7","deadline":"1000","payload":"`) {
		t.Fatal("key order and format")
	}
}

// Design: abstraction/protocol/design.md#minimum-body-size
func TestValidateRequestBodySize(t *testing.T) {
	if ValidateRequestBodySize(75) == nil || ValidateRequestBodySize(76) != nil {
		t.Fatal("request minimum is 76")
	}
}

// Design: abstraction/protocol/design.md#parsing
func TestParseDeadlineTable(t *testing.T) {
	for _, tc := range []struct {
		body string
		want uint64
		ok   bool
	}{
		{`{"id":"7","deadline":"1000","payload":""}`, 1000, true},
		{`{"id":"7","payload":""}`, 0, false},
		{`{"id":"7","deadline":1000}`, 0, false},
		{`{"id":"7","deadline":"18446744073709551616"}`, 0, false},
		{`{"id":"7","deadline":""}`, 0, false},
		{`not json`, 0, false},
	} {
		got, ok := ParseDeadline([]byte(tc.body))
		if got != tc.want || ok != tc.ok {
			t.Fatalf("%s: %d %v", tc.body, got, ok)
		}
		if id, err := ParseBody([]byte(tc.body)); tc.body != "not json" && (err != nil || id != 7) {
			t.Fatalf("%s: ParseBody must ignore the deadline", tc.body)
		}
	}
}
