// Source: abstraction/protocol/design.md (protocol v0.2.1)
package protocol

import "testing"

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
