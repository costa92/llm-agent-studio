package generate

import "context"

// tinyPNG is a 1x1 truecolor PNG (a single red pixel). It is a complete, valid
// image so a keyless dev/demo run produces media the blob store + downstream
// (pending_acceptance → review → library) can handle without nil-panics.
var tinyPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xde, 0x00, 0x00, 0x00,
	0x0c, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0xf8, 0xcf, 0xc0, 0x00,
	0x00, 0x03, 0x01, 0x01, 0x00, 0xc9, 0xfe, 0x92, 0xef, 0x00, 0x00, 0x00,
	0x00, 0x49, 0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
}

// DevFakeGenerator is a keyless dev/demo MediaGenerator: it returns a small valid
// PNG for every request so a deployment with NO provider API keys can run the
// whole pipeline (Run → script/storyboard → asset → pending_acceptance → review
// → library) end-to-end. It is NOT a test double — it is the runtime backing for
// fake mode (PROVIDER=fake / STUDIO_FAKE_GEN=1). Production keyed runs never use it.
type DevFakeGenerator struct{}

// NewDevFakeGenerator builds a DevFakeGenerator.
func NewDevFakeGenerator() *DevFakeGenerator { return &DevFakeGenerator{} }

// Kind reports "image" (the sync path; fake mode produces placeholder images only).
func (g *DevFakeGenerator) Kind() string { return "image" }

// Generate returns the placeholder PNG with deterministic provider/model + the
// usage fields the worker's cost ledger reads (ImageCount feeds RecordPriced).
func (g *DevFakeGenerator) Generate(context.Context, GenRequest) (GenResult, error) {
	return GenResult{
		Bytes:      tinyPNG,
		MimeType:   "image/png",
		Provider:   "fake",
		Model:      "fake",
		ImageCount: 1,
	}, nil
}
