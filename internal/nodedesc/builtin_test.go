package nodedesc

import (
	"encoding/json"
	"testing"
)

func descByType(t *testing.T) map[string]NodeTypeDescription {
	t.Helper()
	m := map[string]NodeTypeDescription{}
	for _, d := range Builtins() {
		if _, dup := m[d.Type]; dup {
			t.Fatalf("duplicate built-in type %q", d.Type)
		}
		m[d.Type] = d
	}
	return m
}

func TestBuiltinsCoverAllSevenTypes(t *testing.T) {
	m := descByType(t)
	for _, want := range []string{
		"studio.script", "studio.storyboard", "studio.asset", "studio.prescreen",
		"llm", "http", "script",
	} {
		d, ok := m[want]
		if !ok {
			t.Fatalf("Builtins() missing %q", want)
		}
		if d.Version != Version {
			t.Errorf("%s.Version=%d, want %d", want, d.Version, Version)
		}
		if d.Label == "" || d.Group == "" {
			t.Errorf("%s missing Label/Group", want)
		}
	}
}

func TestScriptDescriptionAgeBandCascadeAndOutputSchema(t *testing.T) {
	d := descByType(t)["studio.script"]
	var ageBand *Property
	for i := range d.Properties {
		if d.Properties[i].Name == "ageBand" {
			ageBand = &d.Properties[i]
		}
	}
	if ageBand == nil {
		t.Fatal("studio.script has no ageBand property")
	}
	if ageBand.DefaultFrom == nil || ageBand.DefaultFrom.Field != "ageBand" {
		t.Fatal("ageBand.DefaultFrom not wired to itself")
	}
	for band, want := range map[string]int{"0-3": 8, "3-6": 16, "6-8": 16} {
		raw, ok := ageBand.DefaultFrom.Map[band]["pageCount"]
		if !ok {
			t.Fatalf("ageBand cascade missing pageCount for %q", band)
		}
		var got int
		_ = json.Unmarshal(raw, &got)
		if got != want {
			t.Errorf("ageBand %q pageCount=%d, want %d", band, got, want)
		}
	}
	if ageBand.DisplayOptions == nil || len(ageBand.DisplayOptions.Show["pictureBook"]) == 0 {
		t.Error("ageBand should be gated on pictureBook=true via displayOptions")
	}
	wantOut := map[string]bool{"title": false, "logline": false, "characterSheet": false, "scenes": false}
	for _, o := range d.OutputSchema {
		if _, ok := wantOut[o.Name]; ok {
			wantOut[o.Name] = true
		}
	}
	for name, found := range wantOut {
		if !found {
			t.Errorf("studio.script OutputSchema missing %q", name)
		}
	}
}

func TestHttpDescriptionConstraints(t *testing.T) {
	d := descByType(t)["http"]
	var url, headers *Property
	for i := range d.Properties {
		switch d.Properties[i].Name {
		case "url":
			url = &d.Properties[i]
		case "headers":
			headers = &d.Properties[i]
		}
	}
	if url == nil || url.Constraints == nil || !url.Constraints.NoTemplate {
		t.Error("http url must carry Constraints.NoTemplate")
	}
	if headers == nil || headers.Type != PropertyKeyValue {
		t.Error("http headers must be a keyValue property")
	}
}

func TestReservedNamespace(t *testing.T) {
	for _, slug := range []string{"studio.foo", "studio.script", "llm", "http", "script"} {
		if !ReservedNamespace(slug) {
			t.Errorf("ReservedNamespace(%q)=false, want true", slug)
		}
	}
	for _, slug := range []string{"translate", "my-node", "summarize"} {
		if ReservedNamespace(slug) {
			t.Errorf("ReservedNamespace(%q)=true, want false", slug)
		}
	}
}

func TestRegistryOnlyMarkedOnDangerousFields(t *testing.T) {
	want := map[string]map[string]bool{
		"http":   {"url": true, "headers": true, "bodyTemplate": true, "allowResponseBody": true},
		"script": {"code": true},
	}
	for _, d := range Builtins() {
		exp, ok := want[d.Type]
		if !ok {
			continue
		}
		got := map[string]bool{}
		for _, p := range d.Properties {
			if p.Constraints != nil && p.Constraints.RegistryOnly {
				got[p.Name] = true
			}
		}
		for name := range exp {
			if !got[name] {
				t.Errorf("%s.%s must be RegistryOnly", d.Type, name)
			}
		}
		// allowResponseBody is a PLAIN bool with no other constraint — assert the
		// marker still lands (spec §6.3: the no-constraint exfil-launcher hole).
		if d.Type == "http" && !got["allowResponseBody"] {
			t.Error("http.allowResponseBody (no other constraint) must still be RegistryOnly")
		}
	}
}
