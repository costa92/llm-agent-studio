package planner

import "testing"

func TestIsTypeAllowedBuiltin(t *testing.T) {
	for _, typ := range []string{"script", "storyboard", "asset"} {
		if !isTypeAllowed(typ) {
			t.Errorf("isTypeAllowed(%q)=false, want true", typ)
		}
	}
	if isTypeAllowed("nope") {
		t.Error("isTypeAllowed(\"nope\")=true, want false")
	}
}

// TestRegisterTypeMutatesDerivedMap guards that the whitelist derived from
// builtinnode.Types() is still independently mutable — RegisterType must not
// corrupt the catalog's shared state, and the registration must take effect.
func TestRegisterTypeMutatesDerivedMap(t *testing.T) {
	if isTypeAllowed("translate") {
		t.Fatal("translate already whitelisted before RegisterType")
	}
	RegisterType("translate")
	if !isTypeAllowed("translate") {
		t.Fatal("isTypeAllowed(\"translate\")=false after RegisterType")
	}
}
