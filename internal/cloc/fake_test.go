package cloc

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The fake is only useful if it splits the tree the same way cloc does.
func TestFakeRunnerSplitsProductAndTestAsComplements(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "src/app.js", "const a = 1\n\nconst b = 2\n")
	write(t, dir, "src/ui/panel.js", "function panel() {\n  return null\n}\n")
	write(t, dir, "src/app.test.js", "test('a', () => {\n  expect(1).toBe(1)\n})\n")

	r := &FakeRunner{}
	ctx := context.Background()
	regex := DefaultTestRegex

	product, err := r.Count(ctx, dir, "src", Options{TestRegex: regex})
	if err != nil {
		t.Fatal(err)
	}
	test, err := r.Count(ctx, dir, "src", Options{TestRegex: regex, OnlyTests: true})
	if err != nil {
		t.Fatal(err)
	}
	total, err := r.Count(ctx, dir, "src", Options{})
	if err != nil {
		t.Fatal(err)
	}

	if product.Count.Files != 2 || test.Count.Files != 1 {
		t.Errorf("files: product %d, test %d, want 2 and 1", product.Count.Files, test.Count.Files)
	}
	if got := product.Count.Add(test.Count); got != total.Count {
		t.Errorf("product+test = %+v, want %+v", got, total.Count)
	}
	if r.Calls() != 3 {
		t.Errorf("Calls() = %d, want 3", r.Calls())
	}
}

func TestFakeRunnerReportsAnAbsentTreeAsEmpty(t *testing.T) {
	got, err := (&FakeRunner{}).Count(context.Background(), t.TempDir(), "nope", Options{})
	if err != nil {
		t.Fatalf("Count() error = %v, want a clean zero", err)
	}
	if !got.Empty || got.Count.Code != 0 {
		t.Errorf("got %+v, want an empty zero count", got)
	}
}

func TestFakeRunnerHonoursInjectedErrors(t *testing.T) {
	boom := errors.New("boom")
	r := &FakeRunner{Err: func(string) error { return boom }}

	if _, err := r.Count(context.Background(), t.TempDir(), "src", Options{}); !errors.Is(err, boom) {
		t.Errorf("Count() error = %v, want %v", err, boom)
	}
}

func TestFakeRunnerHonoursContextCancellationDuringDelay(t *testing.T) {
	r := &FakeRunner{Delay: func(string) time.Duration { return time.Hour }}
	ctx, cancel := context.WithCancel(context.Background())
	go cancel()

	if _, err := r.Count(ctx, t.TempDir(), "src", Options{}); !errors.Is(err, context.Canceled) {
		t.Errorf("Count() error = %v, want context.Canceled", err)
	}
}
