// Package tree materialises one commit's source folder into a scratch
// directory without touching the repository or the user's working tree.
package tree

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Result describes what an extraction produced.
//
// Files exists because of the bind-mount trap: a scratch directory that mounts
// as empty inside the container makes cloc report zero for every commit,
// silently. Knowing the tree was non-empty on the host turns that into a
// detectable contradiction rather than a plausible-looking graph of zeroes.
type Result struct {
	Found bool // the folder exists at this commit
	Files int  // regular files written to dest
}

// Extract copies folder as it existed at sha into dest.
//
// Content lands at dest/<folder>, because git archive reproduces the path
// prefix. An empty folder extracts the whole tree. A folder that does not exist
// at this commit returns Found=false and no error: early commits legitimately
// predate the source directory.
//
// git archive is used rather than checkout or worktree because it is the only
// one of the three that never mutates anything — checkout would destroy
// uncommitted work, and worktree leaves state under .git that leaks if the
// process dies. Restricting it to a pathspec also keeps each extraction to the
// source folder instead of the whole repository.
func Extract(repo, sha, folder, dest string) (Result, error) {
	folder = strings.TrimSuffix(folder, "/")

	found, err := exists(repo, sha, folder)
	if err != nil {
		return Result{}, err
	}
	if !found {
		return Result{Found: false}, nil
	}

	args := []string{"-C", repo, "archive", "--format=tar", sha}
	if folder != "" {
		args = append(args, "--", folder)
	}

	var stderr bytes.Buffer
	cmd := exec.Command("git", args...)
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, fmt.Errorf("git archive %s: %w", sha, err)
	}
	if err := cmd.Start(); err != nil {
		return Result{}, fmt.Errorf("git archive %s: %w", sha, err)
	}

	files, untarErr := untar(stdout, dest)
	// Drain anything the reader left behind so git never blocks on a full pipe.
	_, _ = io.Copy(io.Discard, stdout)

	if err := cmd.Wait(); err != nil {
		return Result{}, fmt.Errorf("git archive %s %s: %w: %s",
			sha, folder, err, strings.TrimSpace(stderr.String()))
	}
	if untarErr != nil {
		return Result{}, fmt.Errorf("extract %s %s: %w", sha, folder, untarErr)
	}
	return Result{Found: true, Files: files}, nil
}

// exists reports whether folder is present in the commit's tree.
//
// It verifies the revision first and probes the path second, because git
// reports both failures identically — a full-length hash that does not exist
// yields "path 'src' does not exist in '<sha>'", exactly like a real commit
// missing that folder. Splitting the question in two means the path probe's
// exit status can be trusted on its own, with no error-message matching.
func exists(repo, sha, folder string) (bool, error) {
	var stderr bytes.Buffer
	verify := exec.Command("git", "-C", repo, "rev-parse", "--verify", "--quiet", sha+"^{commit}")
	verify.Stderr = &stderr
	if err := verify.Run(); err != nil {
		return false, fmt.Errorf("resolve revision %s in %s: %w: %s",
			sha, repo, err, strings.TrimSpace(stderr.String()))
	}

	probe := exec.Command("git", "-C", repo, "cat-file", "-e", sha+":"+folder)
	probe.Stderr = io.Discard
	if err := probe.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return false, nil
		}
		return false, fmt.Errorf("probe %s:%s in %s: %w", sha, folder, repo, err)
	}
	return true, nil
}

// untar writes a git archive stream into dest and returns the number of
// regular files created.
func untar(r io.Reader, dest string) (int, error) {
	root, err := filepath.Abs(dest)
	if err != nil {
		return 0, err
	}

	var files int
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return files, nil
		}
		if err != nil {
			return files, err
		}

		target, err := safeJoin(root, hdr.Name)
		if err != nil {
			return files, err
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return files, err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return files, err
			}
			if err := writeFile(target, tr, hdr.FileInfo().Mode()); err != nil {
				return files, err
			}
			files++
		case tar.TypeSymlink:
			// Recreated only when it stays inside the scratch directory, so a
			// repository cannot point the counter at arbitrary host files.
			if _, err := safeJoin(root, filepath.Join(filepath.Dir(hdr.Name), hdr.Linkname)); err != nil {
				continue
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return files, err
			}
			if err := os.Symlink(hdr.Linkname, target); err != nil && !os.IsExist(err) {
				return files, err
			}
		}
	}
}

func writeFile(path string, r io.Reader, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode.Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// safeJoin rejects archive entries that would escape the destination.
func safeJoin(root, name string) (string, error) {
	target := filepath.Join(root, filepath.Clean("/"+name))
	if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("archive entry %q escapes the destination directory", name)
	}
	return target, nil
}
