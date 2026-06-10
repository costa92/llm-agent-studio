package config

import "testing"

func TestLoadRequiresPGAndJWT(t *testing.T) {
	_, err := LoadFromLookup(func(string) (string, bool) { return "", false })
	if err == nil {
		t.Fatalf("want error when PG_URL/JWT_SECRET missing")
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := LoadFromLookup(func(k string) (string, bool) {
		switch k {
		case "PG_URL":
			return "postgres://x", true
		case "JWT_SECRET":
			return "s", true
		}
		return "", false
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.HTTPAddr == "" || cfg.Workers <= 0 {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
}
