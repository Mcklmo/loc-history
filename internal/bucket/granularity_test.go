package bucket

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestParseGranularity(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want Granularity
	}{
		{"hour", GranularityHour},
		{"day", GranularityDay},
		{"HOUR", GranularityHour},
		{" day ", GranularityDay},
		{"4h", 4},
		{"12H", 12},
		// The words and the widths are one vocabulary, not two.
		{"1h", GranularityHour},
		{"24h", GranularityDay},
	} {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseGranularity(tt.in)
			if err != nil {
				t.Fatalf("ParseGranularity(%q) error = %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("ParseGranularity(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseGranularityRejectsBucketsThatDoNotTileADay(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want string // a fragment the message has to carry
	}{
		{"week", "hour, day"},
		{"", "hour, day"},
		{"h", "hour, day"},
		{"4x", "hour, day"},
		{"4hours", "hour, day"},
		// Uniform slots are the axis's whole premise: 5h would restart at
		// every midnight and put buckets between the slots.
		{"5h", "divide the day"},
		{"7h", "divide the day"},
		{"48h", "divide the day"},
		{"0h", "divide the day"},
		{"-4h", "divide the day"},
	} {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseGranularity(tt.in)
			if err == nil {
				t.Fatalf("ParseGranularity(%q) = %d, want an error", tt.in, got)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q should mention %q", err, tt.want)
			}
		})
	}
}

// Every accepted bucket has to tile a day exactly, or the axis lattice breaks.
func TestEveryAcceptedGranularityTilesADay(t *testing.T) {
	for n := 1; n <= 48; n++ {
		g := Granularity(n)
		if got := g.Valid(); got != (n <= 24 && 24%n == 0) {
			t.Errorf("Granularity(%d).Valid() = %v", n, got)
		}
		if !g.Valid() {
			continue
		}
		// Walking a full day in steps must land back on midnight having
		// visited only whole buckets.
		midnight := time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC)
		var slots int
		for at := midnight; at.Before(midnight.AddDate(0, 0, 1)); at = at.Add(g.Step()) {
			if !g.Truncate(at).Equal(at) {
				t.Errorf("%d-hour bucket: slot %s is not its own bucket start", n, at.Format("15:04"))
			}
			slots++
		}
		if slots != 24/n {
			t.Errorf("%d-hour bucket: %d slots in a day, want %d", n, slots, 24/n)
		}
	}
}

// A multi-hour bucket floors to a multiple of its width, counting from
// midnight — not to the commit's own hour.
func TestGranularityFloorsToTheBucketBoundary(t *testing.T) {
	for _, tt := range []struct {
		gran Granularity
		at   string
		want string
	}{
		{GranularityHour, "14:30", "14:00"},
		{4, "14:30", "12:00"},
		{4, "03:59", "00:00"},
		{4, "23:59", "20:00"},
		{6, "13:00", "12:00"},
		{12, "13:00", "12:00"},
		{GranularityDay, "23:59", "00:00"},
	} {
		t.Run(fmt.Sprintf("%dh_%s", tt.gran, tt.at), func(t *testing.T) {
			at, err := time.Parse("2006-01-02 15:04", "2026-03-03 "+tt.at)
			if err != nil {
				t.Fatal(err)
			}
			if got := tt.gran.Truncate(at).Format("15:04"); got != tt.want {
				t.Errorf("Truncate(%s) = %s, want %s", tt.at, got, tt.want)
			}
		})
	}
}

// A commit is bucketed by the wall clock its author saw, and the bucket is
// relabelled UTC so every step downstream is exactly one hour.
func TestBucketingKeepsTheCommitsOwnWallClock(t *testing.T) {
	berlin := time.FixedZone("CEST", 2*60*60)
	at := time.Date(2026, 8, 9, 23, 30, 0, 0, berlin)

	hour := GranularityHour.Truncate(at)
	if got := hour.Format("2006-01-02 15:04 MST"); got != "2026-08-09 23:00 UTC" {
		t.Errorf("hour bucket = %s, want the author's own 23:00 relabelled UTC", got)
	}
	if day := GranularityDay.Truncate(at); day.Format("2006-01-02 15:04 MST") != "2026-08-09 00:00 UTC" {
		t.Errorf("day bucket = %s, want 2026-08-09 00:00 UTC", day.Format("2006-01-02 15:04 MST"))
	}
}

// The vocabulary the sinks render: prose noun, table heading, and the two time
// formats. A width that is neither hour nor day names itself in hours.
func TestGranularityNamesItself(t *testing.T) {
	for _, tt := range []struct {
		gran   Granularity
		noun   string
		column string
		row    string
	}{
		{GranularityHour, "hour", "Hour", "2026-03-03 09:00"},
		{GranularityDay, "day", "Date", "2026-03-03"},
		{4, "4 hours", "Bucket start", "2026-03-03 08:00"},
	} {
		t.Run(tt.noun, func(t *testing.T) {
			if got := tt.gran.Noun(); got != tt.noun {
				t.Errorf("Noun() = %q, want %q", got, tt.noun)
			}
			if got := tt.gran.Column(); got != tt.column {
				t.Errorf("Column() = %q, want %q", got, tt.column)
			}
			at := time.Date(2026, 3, 3, 9, 15, 0, 0, time.UTC)
			if got := tt.gran.Truncate(at).Format(tt.gran.RowFormat()); got != tt.row {
				t.Errorf("row cell = %q, want %q", got, tt.row)
			}
		})
	}
}
