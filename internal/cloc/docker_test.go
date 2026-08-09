package cloc

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func requireDocker(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping container test in -short mode")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker daemon not available")
	}
}

func TestDockerArgs(t *testing.T) {
	tests := []struct {
		name    string
		hostDir string
		folder  string
		opts    Options
		want    []string
	}{
		{
			name:    "product excludes test files",
			hostDir: "/tmp/scratch1",
			folder:  "src",
			opts:    Options{TestRegex: `\.test\.js$`, Image: "aldanial/cloc"},
			want: []string{
				"run", "--rm", "-v", "/tmp/scratch1:" + mountPoint, "aldanial/cloc",
				"--json", "--quiet", `--not-match-f=\.test\.js$`, mountPoint + "/src",
			},
		},
		{
			name:    "tests use the complementary flag over the same regex",
			hostDir: "/tmp/scratch1",
			folder:  "src",
			opts:    Options{TestRegex: `\.test\.js$`, Image: "aldanial/cloc", OnlyTests: true},
			want: []string{
				"run", "--rm", "-v", "/tmp/scratch1:" + mountPoint, "aldanial/cloc",
				"--json", "--quiet", `--match-f=\.test\.js$`, mountPoint + "/src",
			},
		},
		{
			name:    "no regex means no filter",
			hostDir: "/tmp/scratch1",
			folder:  "src",
			opts:    Options{Image: "aldanial/cloc"},
			want: []string{
				"run", "--rm", "-v", "/tmp/scratch1:" + mountPoint, "aldanial/cloc",
				"--json", "--quiet", mountPoint + "/src",
			},
		},
		{
			name:    "an empty folder counts the whole mount",
			hostDir: "/tmp/scratch1",
			folder:  "",
			opts:    Options{Image: "aldanial/cloc"},
			want: []string{
				"run", "--rm", "-v", "/tmp/scratch1:" + mountPoint, "aldanial/cloc",
				"--json", "--quiet", mountPoint,
			},
		},
		{
			name:    "nested folders keep their prefix",
			hostDir: "/tmp/scratch1",
			folder:  "packages/web/src",
			opts:    Options{Image: "custom/cloc:2"},
			want: []string{
				"run", "--rm", "-v", "/tmp/scratch1:" + mountPoint, "custom/cloc:2",
				"--json", "--quiet", mountPoint + "/packages/web/src",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dockerArgs(tt.hostDir, tt.folder, tt.opts)
			if strings.Join(got, "\x00") != strings.Join(tt.want, "\x00") {
				t.Errorf("dockerArgs()\n got: %v\nwant: %v", got, tt.want)
			}
		})
	}
}

// The in-container path must be absolute so nothing depends on the image's
// WORKDIR, which is undocumented.
func TestDockerArgsAlwaysPassesAnAbsolutePath(t *testing.T) {
	args := dockerArgs("/tmp/x", "src", Options{Image: "aldanial/cloc"})
	target := args[len(args)-1]
	if !strings.HasPrefix(target, "/") {
		t.Errorf("target path %q is not absolute", target)
	}
}

// A stopped daemon must produce something a human can act on, not a JSON
// parse failure three layers down.
func TestDaemonErrorIsActionable(t *testing.T) {
	stderr := "failed to connect to the docker API at unix:///nonexistent.sock; " +
		"check if the path is correct and if the daemon is running: dial unix"
	err := classifyRunError(errors.New("exit status 1"), stderr)

	if err == nil {
		t.Fatal("classifyRunError() returned nil")
	}
	if !errors.Is(err, ErrDaemonUnavailable) {
		t.Errorf("error %v should be ErrDaemonUnavailable", err)
	}
	if !strings.Contains(err.Error(), "Docker") {
		t.Errorf("error %q should name Docker so the user knows what to start", err)
	}
}

func TestOtherRunErrorsArePassedThroughWithContext(t *testing.T) {
	err := classifyRunError(errors.New("exit status 125"), "docker: invalid reference format")

	if errors.Is(err, ErrDaemonUnavailable) {
		t.Error("an unrelated docker failure was misreported as a stopped daemon")
	}
	if !strings.Contains(err.Error(), "invalid reference format") {
		t.Errorf("error %q lost the underlying stderr", err)
	}
}

func TestDockerRunnerCountsARealTree(t *testing.T) {
	requireDocker(t)

	dir := t.TempDir()
	write(t, dir, "src/app.js", "export const a = 1\n\n// comment\nexport const b = 2\n")
	write(t, dir, "src/ui/panel.js", "export function panel() {\n  return null\n}\n")
	write(t, dir, "src/app.test.js", "import { a } from './app'\n\ntest('a', () => {\n  expect(a).toBe(1)\n})\n")
	write(t, dir, "src/ui/panel.spec.jsx", "test('panel', () => {})\n")

	r := NewDockerRunner()
	regex := `\.(test|spec)\.[mc]?[jt]sx?$`
	ctx := context.Background()

	product, err := r.Count(ctx, dir, "src", Options{TestRegex: regex})
	if err != nil {
		t.Fatalf("product Count() error = %v", err)
	}
	test, err := r.Count(ctx, dir, "src", Options{TestRegex: regex, OnlyTests: true})
	if err != nil {
		t.Fatalf("test Count() error = %v", err)
	}
	total, err := r.Count(ctx, dir, "src", Options{})
	if err != nil {
		t.Fatalf("total Count() error = %v", err)
	}

	if product.Count.Files != 2 || test.Count.Files != 2 {
		t.Errorf("files: product %d, test %d, want 2 and 2", product.Count.Files, test.Count.Files)
	}
	if got := product.Count.Add(test.Count); got != total.Count {
		t.Errorf("product+test = %+v, want the unfiltered total %+v", got, total.Count)
	}
	if product.Version == "" {
		t.Error("Version is empty; the cache key depends on it")
	}
}

func TestDockerRunnerReportsAnEmptyTreeAsZero(t *testing.T) {
	requireDocker(t)

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := NewDockerRunner().Count(context.Background(), dir, "src", Options{})
	if err != nil {
		t.Fatalf("Count() error = %v, want a clean zero", err)
	}
	if got.Count.Code != 0 || !got.Empty {
		t.Errorf("got %+v, want an empty zero count", got)
	}
}

func TestDockerRunnerHonoursContextCancellation(t *testing.T) {
	requireDocker(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	dir := t.TempDir()
	write(t, dir, "src/app.js", "const a = 1\n")

	if _, err := NewDockerRunner().Count(ctx, dir, "src", Options{}); err == nil {
		t.Error("Count() with a cancelled context returned no error")
	}
}

// The preflight canary is what turns the macOS bind-mount trap from a graph
// full of silent zeroes into one clear message at startup.
func TestVerifyMountPassesOnASharedPath(t *testing.T) {
	requireDocker(t)

	version, err := VerifyMount(context.Background(), NewDockerRunner(), "/tmp")
	if err != nil {
		t.Errorf("VerifyMount(/tmp) error = %v, want nil on a shared path", err)
	}
	if version == "" {
		t.Error("VerifyMount returned no cloc version; the cache key depends on it")
	}
}

func TestVerifyMountFailsWhenTheMountShowsUpEmpty(t *testing.T) {
	// A runner that always reports nothing is exactly what a broken bind mount
	// looks like from the outside.
	blind := &FakeRunner{Fn: func(context.Context, string, string, Options) (Output, error) {
		return Output{Empty: true}, nil
	}}

	_, err := VerifyMount(context.Background(), blind, t.TempDir())

	if err == nil {
		t.Fatal("VerifyMount() accepted a mount that reported nothing")
	}
	if !errors.Is(err, ErrMountNotShared) {
		t.Errorf("error %v should be ErrMountNotShared", err)
	}
	if !strings.Contains(err.Error(), "--work-dir") {
		t.Errorf("error %q should point at the flag that fixes it", err)
	}
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
