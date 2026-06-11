// Package image adapts contract/llm.ImageGenerator to the generate.MediaGenerator
// seam (spec §7.2). It handles both delivery paths: providers returning inline
// Bytes (openai b64, google) pass through; providers returning a hosted URL
// (minimax, volcengine) are pulled to bytes so the asset is uniformly addressable
// in the BlobStore (spec §10: 默认拉回落 BlobStore).
package image

import (
	"context"
	"fmt"
	"time"

	"github.com/costa92/llm-agent-contract/llm"
	"github.com/costa92/llm-agent-studio/internal/fetch"
	"github.com/costa92/llm-agent-studio/internal/generate"
)

// Puller fetches a provider-returned URL safely (satisfied by *fetch.Fetcher).
// The seam exists so tests can inject a loopback-permitting fetcher.
type Puller interface {
	Get(ctx context.Context, url string) ([]byte, string, error)
}

// Adapter wraps an llm.ImageGenerator as a generate.MediaGenerator.
type Adapter struct {
	gen    llm.ImageGenerator
	puller Puller
}

// New builds an Adapter. puller pulls URL-delivered images (nil → an SSRF-safe
// fetcher: 30s timeout, 32MB cap, image content types — spec §12 安全加固).
func New(gen llm.ImageGenerator, puller Puller) *Adapter {
	if puller == nil {
		puller = fetch.New(fetch.Config{
			Timeout:             30 * time.Second,
			MaxBytes:            32 << 20,
			AllowedContentTypes: []string{"image/", "application/octet-stream"},
		})
	}
	return &Adapter{gen: gen, puller: puller}
}

// Kind reports "image".
func (a *Adapter) Kind() string { return "image" }

// Generate calls the underlying ImageGenerator and normalizes the first image
// to inline Bytes (pulling a URL if needed), recording provider/model/usage.
func (a *Adapter) Generate(ctx context.Context, req generate.GenRequest) (generate.GenResult, error) {
	start := time.Now()
	resp, err := a.gen.GenerateImage(ctx, llm.ImageRequest{
		Prompt: req.Prompt, N: req.N, Size: req.Size, Quality: req.Quality, Format: req.Format,
	})
	if err != nil {
		return generate.GenResult{}, fmt.Errorf("generate.image: %w", err)
	}
	if len(resp.Images) == 0 {
		return generate.GenResult{}, fmt.Errorf("generate.image: provider returned no images")
	}
	img := resp.Images[0]
	out := generate.GenResult{
		MimeType: img.MimeType, Provider: resp.Provider, Model: resp.Model,
		Tokens: resp.Usage.TotalTokens, ImageCount: len(resp.Images),
		LatencyMS: int(time.Since(start).Milliseconds()),
	}
	if len(img.Bytes) > 0 {
		out.Bytes = img.Bytes
		return out, nil
	}
	if img.URL == "" {
		return generate.GenResult{}, fmt.Errorf("generate.image: image has neither bytes nor url")
	}
	data, ct, err := a.puller.Get(ctx, img.URL)
	if err != nil {
		return generate.GenResult{}, fmt.Errorf("generate.image: pull: %w", err)
	}
	out.Bytes = data
	if out.MimeType == "" {
		out.MimeType = ct
	}
	out.URL = img.URL
	return out, nil
}
