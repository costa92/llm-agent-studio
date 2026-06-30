package worker

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-studio/internal/fetch"
)

// TestRunCustomHTTP_SecretBearingItemsOnlyStatus locks the S-4 invariant on the
// **items** channel (not just content/format). P2a dual-writes node_outputs.items,
// and the workflow-v2 cutover (P3f) makes items the canonical channel and
// eventually DELETES content/format. The existing TestRunCustomHTTP_BodyPolicy
// asserts only the content/format columns under the secret-bearing `{status}`-only
// guard; this test asserts the SAME guard holds on the items column, so a future
// refactor that derived items from the raw response body (instead of the guarded
// `content`) would be caught here.
//
// Worst case modelled: the endpoint ECHOES the secret value back in its body. The
// items column must still carry ONLY {status} — never the body, never the secret.
func TestRunCustomHTTP_SecretBearingItemsOnlyStatus(t *testing.T) {
	if os.Getenv("LLM_AGENT_STUDIO_PG_URL") == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run worker custom tests")
	}
	ctx := context.Background()
	db := assetTestGorm(t)
	orgID := "org_http_" + randHex3()

	// The response body echoes the resolved secret ("tok") + extra payload fields —
	// the strongest S-4 leak case. None of these may reach node_outputs.items.
	const leakyBody = `{"echoed_secret":"tok","leaked_field":"sensitive-value"}`

	_, c := seedHTTPProjectTodo(t, db, orgID)
	w := httpTestWorker(t, db, &fakeSecrets{value: "tok"},
		&fakeDoer{resp: fetch.Response{Status: 200, Body: []byte(leakyBody)}})

	// secret-bearing (Authorization header) + OutputFormat:json + allowResponseBody
	// default false → guard must force {status}-only on BOTH content and items.
	ref, err := w.runCustomHTTP(ctx, c, httpParams{
		Method: "GET", URL: "https://api.example.com", OutputFormat: "json",
		Headers: map[string]string{"Authorization": "Bearer {{secret:K}}"},
	})
	if err != nil {
		t.Fatalf("runCustomHTTP: %v", err)
	}

	outID := strings.TrimPrefix(ref, "custom:")
	var format, content, items string
	if err := db.WithContext(ctx).Raw(
		`SELECT format, content, items::text FROM node_outputs WHERE id=$1`, outID,
	).Row().Scan(&format, &content, &items); err != nil {
		t.Fatalf("load node_output: %v", err)
	}

	// content/format guard (the pre-existing invariant — also asserted here so a
	// regression in the shared guard is unambiguous).
	if format != "http-status" || content != `{"status":200}` {
		t.Fatalf("content guard broken: format=%q content=%q", format, content)
	}

	// items guard (the cutover-critical lock): items reflect ONLY {status}.
	// Negative assertions are the real S-4 proof — the body's secret/payload must
	// be absent from the items channel no matter how items are shaped.
	for _, leaked := range []string{"tok", "echoed_secret", "leaked_field", "sensitive-value"} {
		if strings.Contains(items, leaked) {
			t.Fatalf("S-4 LEAK: items column carries response-body content %q: items=%s", leaked, items)
		}
	}
	// Positive: the status code must be present (so the node still emits something).
	if !strings.Contains(items, "200") {
		t.Fatalf("items lost the {status}: items=%s", items)
	}
	// Consistency: items must NOT be empty array (an empty items would be a
	// different cutover regression — downstream $node/itemsForDep would read nothing).
	if strings.TrimSpace(items) == "[]" {
		t.Fatalf("items is empty array — dual-write dropped the guarded status: items=%s", items)
	}
}
