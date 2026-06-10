// Package image adapts contract/llm.ImageGenerator to the generate.MediaGenerator
// seam (spec §7.2). It handles both delivery paths: providers returning inline
// Bytes (openai b64, google) pass through; providers returning a hosted URL
// (minimax, volcengine) are pulled to bytes so the asset is uniformly addressable
// in the BlobStore (spec §10: 默认拉回落 BlobStore).
package image

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/costa92/llm-agent-contract/llm"
	"github.com/costa92/llm-agent-studio/internal/generate"
)

// Adapter wraps an llm.ImageGenerator as a generate.MediaGenerator.
type Adapter struct {
	gen    llm.ImageGenerator
	client *http.Client
}

// New builds an Adapter. httpClient is used to pull URL-delivered images
// (nil → http.DefaultClient with a 30s timeout).
func New(gen llm.ImageGenerator, httpClient *http.Client) *Adapter {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Adapter{gen: gen, client: httpClient}
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
	data, ct, err := a.pull(ctx, img.URL)
	if err != nil {
		return generate.GenResult{}, err
	}
	out.Bytes = data
	if out.MimeType == "" {
		out.MimeType = ct
	}
	out.URL = img.URL
	return out, nil
}

func (a *Adapter) pull(ctx context.Context, url string) ([]byte, string, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("generate.image: build pull request: %w", err)
	}
	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, "", fmt.Errorf("generate.image: pull: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("generate.image: pull status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("generate.image: read pull body: %w", err)
	}
	return data, resp.Header.Get("Content-Type"), nil
}
