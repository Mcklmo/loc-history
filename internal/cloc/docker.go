package cloc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

// mountPoint is where the scratch directory appears inside the container.
//
// Not /tmp: mounting over the container's own temp directory leaves cloc
// nowhere of its own to write. A dedicated path keeps the two apart.
const mountPoint = "/loc"

// ErrDaemonUnavailable reports that Docker is installed but not running.
var ErrDaemonUnavailable = errors.New("docker daemon unavailable")

// ErrMountNotShared reports that the scratch directory did not appear inside
// the container — the failure that would otherwise show up as a graph of zeroes.
var ErrMountNotShared = errors.New("scratch directory is not visible inside the container")

// DockerRunner counts lines by running cloc in a container.
type DockerRunner struct {
	// Binary is the docker executable; empty means "docker" on PATH.
	Binary string
}

// NewDockerRunner returns a runner that shells out to docker on PATH.
func NewDockerRunner() *DockerRunner { return &DockerRunner{} }

func (d *DockerRunner) Count(ctx context.Context, hostDir, folder string, opts Options) (Output, error) {
	bin := d.Binary
	if bin == "" {
		bin = "docker"
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, dockerArgs(hostDir, folder, opts)...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return Output{}, classifyRunError(err, stderr.String())
	}

	out, err := Parse(stdout.Bytes())
	if err != nil {
		return Output{}, fmt.Errorf("cloc on %s: %w", filepath.Join(hostDir, folder), err)
	}
	return out, nil
}

// dockerArgs builds the container invocation.
//
// The in-container path is always absolute, so nothing depends on the image's
// WORKDIR — which is undocumented and could change between tags.
func dockerArgs(hostDir, folder string, opts Options) []string {
	args := []string{
		"run", "--rm",
		"-v", hostDir + ":" + mountPoint,
		opts.image(),
		"--json", "--quiet",
	}

	if opts.TestRegex != "" {
		flag := "--not-match-f=" // product: everything that is not a test
		if opts.OnlyTests {
			flag = "--match-f=" // test: the exact complement
		}
		args = append(args, flag+opts.TestRegex)
	}

	return append(args, path.Join(mountPoint, folder))
}

// classifyRunError turns docker's stderr into something the user can act on.
func classifyRunError(err error, stderr string) error {
	trimmed := strings.TrimSpace(stderr)
	if isDaemonDown(trimmed) {
		return fmt.Errorf("%w: start Docker Desktop and try again: %s", ErrDaemonUnavailable, trimmed)
	}
	if trimmed == "" {
		return fmt.Errorf("docker run: %w", err)
	}
	return fmt.Errorf("docker run: %w: %s", err, trimmed)
}

func isDaemonDown(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "cannot connect to the docker daemon") ||
		strings.Contains(s, "failed to connect to the docker api") ||
		strings.Contains(s, "is the docker daemon running")
}

// canary is a file cloc is guaranteed to recognise, with a line count that
// cannot be mistaken for noise.
const canaryName = "canary/probe.c"
const canaryBody = "int main(void) {\n  return 0;\n}\n"

// VerifyMount proves that a directory created under workDir is actually
// visible inside the container before the walk begins, and returns the cloc
// version that answered.
//
// This exists because a scratch directory outside Docker Desktop's shared
// paths mounts as *empty* rather than failing: cloc then reports zero for
// every commit, exits zero, and the run produces a plausible-looking graph of
// nothing. cloc answers `{}` identically for "the mount is empty" and "nothing
// matched", so the only way to tell them apart is to ask a question whose
// answer is known in advance.
//
// The version comes back because the cache is keyed on it and would otherwise
// need a second container to find it out.
func VerifyMount(ctx context.Context, r Runner, workDir string) (string, error) {
	dir, err := os.MkdirTemp(workDir, "loc-history-preflight-")
	if err != nil {
		return "", fmt.Errorf("create preflight directory in %s: %w", workDir, err)
	}
	defer os.RemoveAll(dir)

	target := filepath.Join(dir, filepath.FromSlash(canaryName))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(target, []byte(canaryBody), 0o644); err != nil {
		return "", err
	}

	out, err := r.Count(ctx, dir, "canary", Options{})
	if err != nil {
		return "", err
	}
	if out.Count.Code == 0 {
		return "", fmt.Errorf("%w: %s mounted empty inside the container — "+
			"Docker Desktop only shares a fixed set of host paths, so pass --work-dir "+
			"pointing somewhere shared (/tmp works by default)", ErrMountNotShared, workDir)
	}
	return out.Version, nil
}
