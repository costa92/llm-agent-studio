package worker

import "encoding/json"

// Item mirrors n8n INodeExecutionData (spec §3.2): one node-execution datum with a
// structured json payload and optional per-port binary refs. No pairedItem (★D-3).
// json is the canonical typed object (ScriptOutput/Shot/ReviewOutput/parsed-custom),
// NEVER a {text:"<json string>"} wrapper for structured output (★D-6).
type Item struct {
	JSON   json.RawMessage      `json:"json"`
	Binary map[string]BinaryRef `json:"binary,omitempty"`
}

// BinaryRef is a thin pointer into the assets table (spec §3.3); bytes never inline.
// P2a never emits one (asset binary-item emission is P3); the type exists so
// loadInputs round-trips it.
type BinaryRef struct {
	AssetID  string `json:"assetId"`
	MimeType string `json:"mimeType"`
	Kind     string `json:"kind"` // image|video|audio
	Status   string `json:"status,omitempty"` // ★D-4: asset status at materialization
}

func jsonItem(payload json.RawMessage) Item { return Item{JSON: payload} }

func textItem(text string) Item {
	b, _ := json.Marshal(map[string]string{"text": text})
	return Item{JSON: b}
}

// itemsJSON marshals items as a JSON array, NEVER null — node_outputs.items is
// JSONB NOT NULL and the app layer guarantees an array (★D-5).
func itemsJSON(items []Item) ([]byte, error) {
	if items == nil {
		items = []Item{}
	}
	return json.Marshal(items)
}
