package orgsecret

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"testing"

	"gorm.io/gorm"

	"github.com/costa92/llm-agent-studio/internal/secretbox"
	"github.com/costa92/llm-agent-studio/internal/storage"
)

func testGorm(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run org secret store tests")
	}
	ctx := context.Background()
	st, err := storage.Open(ctx, storage.Config{PGURL: dsn})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st.GORM()
}

func randID(t *testing.T) string {
	t.Helper()
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// enabledBox builds a real AES-256-GCM box from a fixed 32-byte key (base64).
func enabledBox(t *testing.T) *secretbox.Box {
	t.Helper()
	// 32 zero bytes, base64. Deterministic key is fine for tests (no secret value asserted plaintext-equal cross-process).
	box, err := secretbox.New("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if err != nil {
		t.Fatalf("box: %v", err)
	}
	return box
}

func TestCreateListGet_NoPlaintext(t *testing.T) {
	db := testGorm(t)
	org := randID(t)
	s := New(db, enabledBox(t))
	sec, err := s.Create(context.Background(), org, UpsertInput{Name: "PARTNER_KEY", Value: "topsecret"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if sec.Name != "PARTNER_KEY" || !sec.HasValue || sec.OrgID != org {
		t.Fatalf("bad DTO: %+v", sec)
	}
	items, err := s.List(context.Background(), org)
	if err != nil || len(items) != 1 {
		t.Fatalf("list: %v len=%d", err, len(items))
	}
	// The OrgSecret DTO has NO Value field at all — the no-plaintext invariant is
	// enforced by the struct shape, so there is nothing to assert here.
}

func TestKeepOrReplace(t *testing.T) {
	db := testGorm(t)
	org := randID(t)
	s := New(db, enabledBox(t))
	_, _ = s.Create(context.Background(), org, UpsertInput{Name: "K", Value: "v1"})
	// Empty value on update keeps the existing ciphertext.
	if _, err := s.Update(context.Background(), org, "K", UpsertInput{Name: "K", Value: ""}); err != nil {
		t.Fatalf("update keep: %v", err)
	}
	got, err := s.Resolve(context.Background(), org, "K")
	if err != nil || got != "v1" {
		t.Fatalf("resolve after keep = %q err=%v (want v1)", got, err)
	}
	// Non-empty value replaces.
	if _, err := s.Update(context.Background(), org, "K", UpsertInput{Name: "K", Value: "v2"}); err != nil {
		t.Fatalf("update replace: %v", err)
	}
	got, _ = s.Resolve(context.Background(), org, "K")
	if got != "v2" {
		t.Fatalf("resolve after replace = %q (want v2)", got)
	}
}

func TestOrgIsolation(t *testing.T) {
	db := testGorm(t)
	orgA, orgB := randID(t), randID(t)
	s := New(db, enabledBox(t))
	_, _ = s.Create(context.Background(), orgA, UpsertInput{Name: "S", Value: "v"})
	// Cross-org resolve fails (does not leak the name's existence beyond ErrNotFound).
	if _, err := s.Resolve(context.Background(), orgB, "S"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-org resolve should be ErrNotFound, got %v", err)
	}
	if _, err := s.Update(context.Background(), orgB, "S", UpsertInput{Name: "S", Value: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-org update should be ErrNotFound, got %v", err)
	}
	if err := s.Delete(context.Background(), orgB, "S"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-org delete should be ErrNotFound, got %v", err)
	}
}

func TestNameUnique(t *testing.T) {
	db := testGorm(t)
	org := randID(t)
	s := New(db, enabledBox(t))
	if _, err := s.Create(context.Background(), org, UpsertInput{Name: "DUP", Value: "a"}); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := s.Create(context.Background(), org, UpsertInput{Name: "DUP", Value: "b"}); err == nil {
		t.Fatalf("duplicate name should fail")
	}
}

func TestBoxDisabled_Refuses(t *testing.T) {
	db := testGorm(t)
	org := randID(t)
	disabled, _ := secretbox.New("") // disabled box
	s := New(db, disabled)
	if _, err := s.Create(context.Background(), org, UpsertInput{Name: "N", Value: "v"}); !errors.Is(err, ErrEncUnavailable) {
		t.Fatalf("box-disabled create should be ErrEncUnavailable, got %v", err)
	}
}
