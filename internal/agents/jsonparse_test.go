package agents

import "testing"

func TestExtractJSONObject(t *testing.T) {
	cases := []struct {
		name, in, want string
		wantErr        bool
	}{
		{"plain", `{"a":1}`, `{"a":1}`, false},
		{"fenced", "```json\n{\"a\":1}\n```", `{"a":1}`, false},
		{"fenced-bare", "```\n{\"a\":1}\n```", `{"a":1}`, false},
		{"leading-noise", "Here is the result:\n{\"a\":1}\nDone.", `{"a":1}`, false},
		{"nested-braces", `prefix {"a":{"b":2}} suffix`, `{"a":{"b":2}}`, false},
		{"no-object", `no json here`, ``, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := extractJSONObject(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("want error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}
