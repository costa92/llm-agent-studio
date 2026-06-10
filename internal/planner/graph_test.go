package planner

import "testing"

func TestValidateAcceptsValidGraph(t *testing.T) {
	g := Graph{Nodes: []Node{
		{ID: "n1", Type: "script", DependsOn: nil},
		{ID: "n2", Type: "storyboard", DependsOn: []string{"n1"}},
	}}
	if err := Validate(g); err != nil {
		t.Fatalf("valid graph rejected: %v", err)
	}
}

func TestValidateRejectsUnknownType(t *testing.T) {
	g := Graph{Nodes: []Node{{ID: "n1", Type: "video", DependsOn: nil}}}
	if err := Validate(g); err == nil {
		t.Fatalf("want error for unknown type")
	}
}

func TestValidateRejectsMissingScript(t *testing.T) {
	g := Graph{Nodes: []Node{{ID: "n1", Type: "storyboard", DependsOn: nil}}}
	if err := Validate(g); err == nil {
		t.Fatalf("want error: graph must contain a script node")
	}
}

func TestValidateRejectsCycle(t *testing.T) {
	g := Graph{Nodes: []Node{
		{ID: "n1", Type: "script", DependsOn: []string{"n2"}},
		{ID: "n2", Type: "storyboard", DependsOn: []string{"n1"}},
	}}
	if err := Validate(g); err == nil {
		t.Fatalf("want error for cyclic graph")
	}
}

func TestValidateRejectsDanglingDependency(t *testing.T) {
	g := Graph{Nodes: []Node{{ID: "n1", Type: "script", DependsOn: []string{"ghost"}}}}
	if err := Validate(g); err == nil {
		t.Fatalf("want error for dangling dependency")
	}
}

func TestValidateRejectsDuplicateID(t *testing.T) {
	g := Graph{Nodes: []Node{
		{ID: "n1", Type: "script", DependsOn: nil},
		{ID: "n1", Type: "storyboard", DependsOn: nil},
	}}
	if err := Validate(g); err == nil {
		t.Fatalf("want error for duplicate node id")
	}
}

func TestParseGraphTolerant(t *testing.T) {
	in := "```json\n{\"nodes\":[{\"id\":\"a\",\"type\":\"script\",\"dependsOn\":[]}]}\n```"
	g, err := ParseGraph(in)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(g.Nodes) != 1 || g.Nodes[0].Type != "script" {
		t.Fatalf("bad parse: %+v", g)
	}
}

func TestParseGraphMalformedErrors(t *testing.T) {
	if _, err := ParseGraph("sorry, no plan"); err == nil {
		t.Fatalf("want error for malformed plan")
	}
}

func TestValidateAcceptsAssetType(t *testing.T) {
	g := Graph{Nodes: []Node{
		{ID: "s", Type: "script", DependsOn: nil},
		{ID: "b", Type: "storyboard", DependsOn: []string{"s"}},
		{ID: "a", Type: "asset", DependsOn: []string{"b"}},
	}}
	if err := Validate(g); err != nil {
		t.Fatalf("asset type should be whitelisted in M2: %v", err)
	}
}

func TestDefaultPipelineIsValid(t *testing.T) {
	g := DefaultPipeline()
	if err := Validate(g); err != nil {
		t.Fatalf("default pipeline invalid: %v", err)
	}
	// must be script → storyboard
	if g.Nodes[0].Type != "script" || g.Nodes[1].Type != "storyboard" {
		t.Fatalf("default pipeline wrong shape: %+v", g)
	}
	if len(g.Nodes[1].DependsOn) != 1 || g.Nodes[1].DependsOn[0] != g.Nodes[0].ID {
		t.Fatalf("storyboard must depend on script: %+v", g)
	}
}
