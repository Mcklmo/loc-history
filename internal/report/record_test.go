package report

import (
	"testing"
	"time"
)

func TestShortSHA(t *testing.T) {
	tests := []struct {
		name string
		sha  string
		want string
	}{
		{"full sha truncates to 7", "08ab753d1c4e5f6a7b8c9d0e1f2a3b4c5d6e7f80", "08ab753"},
		{"already short is unchanged", "08ab75", "08ab75"},
		{"empty stays empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShortSHA(tt.sha); got != tt.want {
				t.Errorf("ShortSHA(%q) = %q, want %q", tt.sha, got, tt.want)
			}
		})
	}
}

func TestCountAdd(t *testing.T) {
	a := Count{Files: 2, Code: 100, Comment: 10, Blank: 5}
	b := Count{Files: 3, Code: 50, Comment: 1, Blank: 2}

	got := a.Add(b)
	want := Count{Files: 5, Code: 150, Comment: 11, Blank: 7}
	if got != want {
		t.Errorf("Add() = %+v, want %+v", got, want)
	}
	if a.Code != 100 {
		t.Errorf("Add mutated the receiver: %+v", a)
	}
}

func TestFinalizeComputesTotalAndDelta(t *testing.T) {
	tests := []struct {
		name      string
		product   Count
		test      Count
		prevTotal int
		wantTotal int
		wantDelta int
	}{
		{
			name:      "growth from nothing",
			product:   Count{Code: 412},
			prevTotal: 0,
			wantTotal: 412,
			wantDelta: 412,
		},
		{
			name:      "product and test both count toward the total",
			product:   Count{Code: 3120},
			test:      Count{Code: 1804},
			prevTotal: 4643,
			wantTotal: 4924,
			wantDelta: 281,
		},
		{
			name:      "a net deletion is negative, not clamped",
			product:   Count{Code: 900},
			test:      Count{Code: 100},
			prevTotal: 1500,
			wantTotal: 1000,
			wantDelta: -500,
		},
		{
			name:      "a commit that touches no counted lines has zero delta",
			product:   Count{Code: 200},
			prevTotal: 200,
			wantTotal: 200,
			wantDelta: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Record{Product: tt.product, Test: tt.test}
			r.Finalize(tt.prevTotal)
			if r.TotalCode != tt.wantTotal {
				t.Errorf("TotalCode = %d, want %d", r.TotalCode, tt.wantTotal)
			}
			if r.Delta != tt.wantDelta {
				t.Errorf("Delta = %d, want %d", r.Delta, tt.wantDelta)
			}
		})
	}
}

// Deltas are a running difference, so summing them must reproduce the final total.
// This is the invariant every sink relies on.
func TestDeltasSumToFinalTotal(t *testing.T) {
	counts := []struct{ product, test int }{
		{412, 0}, {488, 0}, {1200, 300}, {1100, 900}, {3120, 1804},
	}

	var sum, prev int
	var last Record
	for _, c := range counts {
		r := Record{Product: Count{Code: c.product}, Test: Count{Code: c.test}}
		r.Finalize(prev)
		sum += r.Delta
		prev = r.TotalCode
		last = r
	}

	if sum != last.TotalCode {
		t.Errorf("deltas sum to %d, final TotalCode is %d", sum, last.TotalCode)
	}
}

func TestNewRecordDerivesShortSHA(t *testing.T) {
	ts := time.Date(2026, 8, 6, 21, 23, 18, 0, time.UTC)
	c := Commit{
		SHA:       "08ab753d1c4e5f6a7b8c9d0e1f2a3b4c5d6e7f80",
		Timestamp: ts,
		Author:    "mcklmo",
		Subject:   "first commit",
	}

	r := NewRecord(c)

	if r.Short != "08ab753" {
		t.Errorf("Short = %q, want %q", r.Short, "08ab753")
	}
	if r.SHA != c.SHA || !r.Timestamp.Equal(ts) || r.Author != "mcklmo" || r.Subject != "first commit" {
		t.Errorf("NewRecord did not carry the commit fields through: %+v", r)
	}
}
