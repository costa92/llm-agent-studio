package generate

import (
	"context"
	"testing"
)

func TestFakeGeneratorCursorAdvances(t *testing.T) {
	f := NewFake(
		GenResult{Bytes: []byte("img1"), MimeType: "image/png", Provider: "fake", Model: "fake-img", ImageCount: 1, Tokens: 10},
		GenResult{Bytes: []byte("img2"), MimeType: "image/png", Provider: "fake", Model: "fake-img", ImageCount: 1, Tokens: 12},
	)
	if f.Kind() != "image" {
		t.Fatalf("kind = %q want image", f.Kind())
	}
	r1, err := f.Generate(context.Background(), GenRequest{Prompt: "a"})
	if err != nil || string(r1.Bytes) != "img1" {
		t.Fatalf("first: %v %q", err, r1.Bytes)
	}
	r2, _ := f.Generate(context.Background(), GenRequest{Prompt: "b"})
	if string(r2.Bytes) != "img2" {
		t.Fatalf("second: %q", r2.Bytes)
	}
	if _, err := f.Generate(context.Background(), GenRequest{Prompt: "c"}); err == nil {
		t.Fatalf("expected exhaustion error")
	}
}

func TestFakeGeneratorLoopsWhenSingle(t *testing.T) {
	// A single canned result is reused for every call (loop=true), so fan-out
	// over N shots all succeed without scripting N responses.
	f := NewFakeLooping(GenResult{Bytes: []byte("x"), MimeType: "image/png", Provider: "fake", Model: "m", ImageCount: 1})
	for i := 0; i < 5; i++ {
		if r, err := f.Generate(context.Background(), GenRequest{Prompt: "p"}); err != nil || string(r.Bytes) != "x" {
			t.Fatalf("loop call %d: %v %q", i, err, r.Bytes)
		}
	}
}
