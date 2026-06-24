package builtinnode

import "testing"

func TestCatalogHasFourBuiltinTypes(t *testing.T) {
	c := Catalog()
	if len(c) != 4 {
		t.Fatalf("Catalog() len=%d, want 4", len(c))
	}
	want := []string{"script", "storyboard", "asset", "prescreen"}
	for i, w := range want {
		if c[i].Type != w {
			t.Errorf("Catalog()[%d].Type=%q, want %q", i, c[i].Type, w)
		}
	}
}

func TestTypesMatchCatalog(t *testing.T) {
	types := Types()
	if len(types) != len(Catalog()) {
		t.Fatalf("Types() len=%d, Catalog() len=%d", len(types), len(Catalog()))
	}
	for _, b := range Catalog() {
		if !types[b.Type] {
			t.Errorf("Types() missing %q", b.Type)
		}
	}
}

func TestTypesReturnsFreshMap(t *testing.T) {
	first := Types()
	first["injected"] = true
	second := Types()
	if second["injected"] {
		t.Fatal("Types() returned a shared map: mutation leaked across calls")
	}
}
