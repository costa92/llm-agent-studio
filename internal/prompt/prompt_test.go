package prompt

import (
	"strings"
	"testing"
)

func TestStylesReturnsCatalog(t *testing.T) {
	styles := Styles()
	if len(styles) != 7 {
		t.Fatalf("want 7 styles, got %d", len(styles))
	}
	want := map[string]bool{"日漫": true, "吉卜力": true, "皮克斯": true, "迪士尼": true, "写实": true, "赛博朋克": true, "国风": true}
	for _, s := range styles {
		if !want[s.Name] {
			t.Fatalf("unexpected style %q", s.Name)
		}
		if s.Suffix == "" {
			t.Fatalf("style %q has empty suffix", s.Name)
		}
	}
}

func TestBuildAppendsStyleSuffix(t *testing.T) {
	b := NewBuilder()
	out := b.Build("a teahouse at dusk", "国风")
	if !strings.Contains(out, "a teahouse at dusk") {
		t.Fatalf("base prompt missing: %q", out)
	}
	if !strings.Contains(out, "guofeng") && !strings.Contains(out, "chinese") {
		t.Fatalf("style suffix not injected: %q", out)
	}
}

func TestBuildUnknownStylePassesThrough(t *testing.T) {
	b := NewBuilder()
	out := b.Build("a robot", "no-such-style")
	if out != "a robot" {
		t.Fatalf("unknown style should pass base through unchanged, got %q", out)
	}
}

func TestBuildEmptyStyleNoSuffix(t *testing.T) {
	b := NewBuilder()
	if out := b.Build("plain", ""); out != "plain" {
		t.Fatalf("empty style should not append, got %q", out)
	}
}
