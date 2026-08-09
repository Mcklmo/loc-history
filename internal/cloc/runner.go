// Package cloc counts the lines in a materialised source tree.
//
// Runner is an interface specifically so the pipeline and the sinks can be
// tested without Docker and without a network — see FakeRunner.
package cloc

import "context"

// DefaultImage is the published cloc image. Note it is amd64-only, so on Apple
// silicon every invocation runs under emulation.
const DefaultImage = "aldanial/cloc"

// DefaultTestRegex matches .test.js, .spec.ts, .test.tsx, .test.mjs, .spec.cjs
// and friends. cloc applies it to the basename unless --fullpath is given.
const DefaultTestRegex = `\.(test|spec)\.[mc]?[jt]sx?$`

// Runner counts the lines under hostDir/folder.
//
// hostDir is a directory on this machine; folder is the path within it, which
// mirrors the layout git archive produces.
type Runner interface {
	Count(ctx context.Context, hostDir, folder string, opts Options) (Output, error)
}

// Options selects what a single count measures.
type Options struct {
	// TestRegex splits the tree in two. The same expression drives both
	// queries — as --not-match-f for product code and --match-f for test code
	// — which makes them exact complements over the same tree. An empty
	// TestRegex means no filter at all, counting everything.
	TestRegex string

	// OnlyTests picks which side of that split to measure.
	OnlyTests bool

	// Image overrides the container image; empty means DefaultImage.
	Image string
}

func (o Options) image() string {
	if o.Image == "" {
		return DefaultImage
	}
	return o.Image
}
