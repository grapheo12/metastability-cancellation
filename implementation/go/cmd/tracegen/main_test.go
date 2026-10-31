// Source: AGENTS.md#traceability-generator-tracegen
package main

import "testing"

func TestSlugMatchesExistingAnchors(t *testing.T) {
	cases := map[string]string{
		"RequestId":                              "requestid",
		"Minimum body size":                      "minimum-body-size",
		"handle_ping":                            "handle_ping",
		"State machine (per `/ping` StreamTask)": "state-machine-per-ping-streamtask",
		"Status classification (load-generator client)": "status-classification-load-generator-client",
	}
	for h, want := range cases {
		if got := slug(h); got != want {
			t.Errorf("slug(%q)=%q want %q", h, got, want)
		}
	}
}

func TestDedupSorted(t *testing.T) {
	got := dedupSorted([]loc{{"b.go", 2}, {"a.go", 9}, {"b.go", 2}, {"a.go", 1}})
	want := []loc{{"a.go", 1}, {"a.go", 9}, {"b.go", 2}}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%v", got)
		}
	}
}
