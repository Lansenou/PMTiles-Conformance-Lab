package fixtures

import "github.com/lansenou/pmtiles-conformance-lab/internal/pmtiles"

// malformedCase is one deliberately broken (or unsupported) archive. Each
// case changes one thing relative to a valid base archive.
type malformedCase struct {
	name        string
	kind        string // "malformed" or "unsupported"
	description string
	code        pmtiles.Code
	build       func() []byte
}

func baseRootNone() built { return build(validSpecs()[0]) }

func malformedCases() []malformedCase {
	return []malformedCase{
		{
			name: "bad-magic", kind: "malformed", code: pmtiles.CodeBadMagic,
			description: "root-none with the first magic byte changed from 'P' to 'X'.",
			build: func() []byte {
				b := baseRootNone().bytes
				b[0] = 'X'
				return b
			},
		},
	}
}
