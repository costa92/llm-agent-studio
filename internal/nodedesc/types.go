// Package nodedesc is the single source of truth for declarative workflow node
// type descriptions (n8n INodeTypeDescription shape). Leaf package: imports only
// stdlib so any studio package can depend on it without an import cycle. P1 uses
// it for read-only rendering + the GET /api/node-types merge; it does NOT own the
// save path (canvasModel.toStudioNodes is unchanged) and does NOT model color
// (color is a frontend/theme concern, see web nodeColor.ts).
package nodedesc

import "encoding/json"

const Version = 1

type PropertyType string

const (
	PropertyString          PropertyType = "string"
	PropertyTextarea        PropertyType = "textarea"
	PropertyNumber          PropertyType = "number"
	PropertyBoolean         PropertyType = "boolean"
	PropertyOptions         PropertyType = "options"
	PropertyCollection      PropertyType = "collection"
	PropertyFixedCollection PropertyType = "fixedCollection"
	PropertyJSON            PropertyType = "json"
	PropertyCode            PropertyType = "code"
	PropertyPrompt          PropertyType = "prompt"
	PropertyKeyValue        PropertyType = "keyValue"
	PropertyResourceLocator PropertyType = "resourceLocator"
)

type NodeTypeDescription struct {
	Type         string        `json:"type"`
	Version      int           `json:"version"`
	Label        string        `json:"label"`
	Description  string        `json:"description"`
	Group        string        `json:"group"`
	Inputs       []PortSpec    `json:"inputs"`
	Outputs      []PortSpec    `json:"outputs"`
	OutputSchema []OutputField `json:"outputSchema,omitempty"`
	Properties   []Property    `json:"properties"`
}

type PortSpec struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type OutputField struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Desc string `json:"desc,omitempty"`
}

type Property struct {
	Name           string          `json:"name"`
	Label          string          `json:"label"`
	Type           PropertyType    `json:"type"`
	Default        json.RawMessage `json:"default,omitempty"`
	DefaultFrom    *DerivedDefault `json:"defaultFrom,omitempty"`
	Required       bool            `json:"required,omitempty"`
	Options        []OptionItem    `json:"options,omitempty"`
	DisplayOptions *DisplayOptions `json:"displayOptions,omitempty"`
	TypeOptions    *TypeOptions    `json:"typeOptions,omitempty"`
	Constraints    *Constraints    `json:"constraints,omitempty"`
	Placeholder    string          `json:"placeholder,omitempty"`
}

type OptionItem struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type DerivedDefault struct {
	Field string                                `json:"field"`
	Map   map[string]map[string]json.RawMessage `json:"map"`
}

type DisplayOptions struct {
	Show map[string][]json.RawMessage `json:"show,omitempty"`
	Hide map[string][]json.RawMessage `json:"hide,omitempty"`
}

type TypeOptions struct {
	Rows       int    `json:"rows,omitempty"`
	Editor     string `json:"editor,omitempty"`
	Password   bool   `json:"password,omitempty"`
	DataSource string `json:"dataSource,omitempty"`
	PromptKind string `json:"promptKind,omitempty"`
}

type Constraints struct {
	NoTemplate      bool     `json:"noTemplate,omitempty"`
	NoSecret        bool     `json:"noSecret,omitempty"`
	SecretAllowedIn []string `json:"secretAllowedIn,omitempty"`
	// RegistryOnly marks a field that a per-node parameters overlay may NEVER set
	// (spec §6.3, M1). The single source of truth for "danger" — covers the
	// no-constraint exfil launcher (http.allowResponseBody) that no danger
	// Constraint alone would catch.
	RegistryOnly bool `json:"registryOnly,omitempty"`
}
