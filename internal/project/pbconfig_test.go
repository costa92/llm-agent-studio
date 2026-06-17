package project

import "testing"

// TestParsePictureBookConfig_AgeDefaults: 仅给 ageBand 时，按年龄段填默认
// （页数 / 旁白风格），MaxWordsPerSpread 由年龄段决定。
func TestParsePictureBookConfig_AgeDefaults(t *testing.T) {
	c, err := ParsePictureBookConfig(`{"ageBand":"3-6"}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.PageCount != 16 {
		t.Fatalf("PageCount=%d want 16", c.PageCount)
	}
	if c.NarrationStyle != "plain" {
		t.Fatalf("NarrationStyle=%q want plain", c.NarrationStyle)
	}
	if c.MaxWordsPerSpread() != 50 {
		t.Fatalf("MaxWordsPerSpread=%d want 50", c.MaxWordsPerSpread())
	}
}

// TestParsePictureBookConfig_RespectsOverrides: 显式给出的字段保留，不被默认覆盖；
// MaxWordsPerSpread 仍由 ageBand 决定。
func TestParsePictureBookConfig_RespectsOverrides(t *testing.T) {
	c, err := ParsePictureBookConfig(`{"ageBand":"0-3","pageCount":12,"narrationStyle":"rhyming","bookType":"concept","illustrationStyle":"watercolor","themes":["friendship"],"voice":"warm"}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.PageCount != 12 {
		t.Fatalf("PageCount=%d want 12 (override)", c.PageCount)
	}
	if c.NarrationStyle != "rhyming" {
		t.Fatalf("NarrationStyle=%q want rhyming (override)", c.NarrationStyle)
	}
	if c.BookType != "concept" {
		t.Fatalf("BookType=%q want concept", c.BookType)
	}
	if c.MaxWordsPerSpread() != 10 {
		t.Fatalf("MaxWordsPerSpread=%d want 10", c.MaxWordsPerSpread())
	}
}

// TestParsePictureBookConfig_EmptyIsZeroValue: 空串 → 零值结构，无错。
func TestParsePictureBookConfig_EmptyIsZeroValue(t *testing.T) {
	c, err := ParsePictureBookConfig("")
	if err != nil {
		t.Fatalf("parse empty: %v", err)
	}
	if c.AgeBand != "" || c.BookType != "" || c.NarrationStyle != "" || c.PageCount != 0 || len(c.Themes) != 0 {
		t.Fatalf("empty input should yield zero value, got %+v", c)
	}
}
