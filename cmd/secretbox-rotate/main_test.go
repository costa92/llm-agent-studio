package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/costa92/llm-agent-studio/internal/secretbox"
	"github.com/costa92/llm-agent-studio/internal/storage"
)

// Two distinct base64 32-byte AES-256 keys for the rotation under test.
const (
	keyA = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" // 32 zero bytes
	keyB = "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=" // 32 0x01 bytes
)

func rotateTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run secretbox-rotate tests")
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
	return st.Pool()
}

// TestRotateReencryptsAcrossKeys proves the full rotation: rows encrypted under
// keyA become decryptable under keyB (and only keyB) after -commit, dry-run is a
// no-op, and a wrong old key aborts without touching anything.
func TestRotateReencryptsAcrossKeys(t *testing.T) {
	pool := rotateTestPool(t)
	ctx := context.Background()
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")

	boxA, _ := secretbox.New(keyA)
	boxB, _ := secretbox.New(keyB)

	// The tool rotates the WHOLE table — so the test must own the whole encrypted
	// set. Clear any rows left under a stray key by a prior run of a reused DB; a
	// mixed-key table is exactly the invalid state the tool (correctly) refuses.
	mustExec(t, pool, `DELETE FROM model_configs WHERE api_key_enc IS NOT NULL`)
	mustExec(t, pool, `DELETE FROM storage_configs WHERE secret_enc IS NOT NULL`)
	mustExec(t, pool, `DELETE FROM mail_configs WHERE smtp_pass_enc IS NOT NULL`)

	// Seed one encrypted row in each of the three covered tables (unique ids so the
	// test is isolated in a reused DB).
	modelID, storageID, mailID := "rot_model_"+rand8(), "rot_storage_"+rand8(), "rot_mail_"+rand8()
	encModel, _ := boxA.Encrypt([]byte("model-secret"))
	encStorage, _ := boxA.Encrypt([]byte("storage-secret"))
	encMail, _ := boxA.Encrypt([]byte("smtp-secret"))
	mustExec(t, pool, `INSERT INTO model_configs (id, org_id, provider, model, api_key_enc) VALUES ($1,$2,'openai','gpt',$3)`, modelID, "rot_org_"+rand8(), encModel)
	mustExec(t, pool, `INSERT INTO storage_configs (id, scope, org_id, mode, secret_enc) VALUES ($1,'org',$2,'s3',$3)`, storageID, "rot_org_"+rand8(), encStorage)
	mustExec(t, pool, `INSERT INTO mail_configs (id, scope, smtp_pass_enc) VALUES ($1,$2,$3)`, mailID, "rot_scope_"+rand8(), encMail)

	// Wrong old key: must error and change nothing.
	if err := run(ctx, keyB, keyA, dsn, true); err == nil {
		t.Fatalf("rotation with wrong old key must fail (decrypt should not succeed)")
	}
	assertDecrypts(t, pool, boxA, modelID, "model_configs", "api_key_enc", "model-secret") // still keyA

	// Dry-run: reports work but writes nothing — row still under keyA.
	if err := run(ctx, keyA, keyB, dsn, false); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	assertDecrypts(t, pool, boxA, modelID, "model_configs", "api_key_enc", "model-secret")

	// Commit: every covered column flips to keyB.
	if err := run(ctx, keyA, keyB, dsn, true); err != nil {
		t.Fatalf("commit: %v", err)
	}
	assertDecrypts(t, pool, boxB, modelID, "model_configs", "api_key_enc", "model-secret")
	assertDecrypts(t, pool, boxB, storageID, "storage_configs", "secret_enc", "storage-secret")
	assertDecrypts(t, pool, boxB, mailID, "mail_configs", "smtp_pass_enc", "smtp-secret")

	// And the old key can no longer read the rotated row.
	var cipher []byte
	_ = pool.QueryRow(ctx, `SELECT api_key_enc FROM model_configs WHERE id=$1`, modelID).Scan(&cipher)
	if _, err := boxA.Decrypt(cipher); err == nil {
		t.Fatalf("old key must NOT decrypt after rotation")
	}
}

func assertDecrypts(t *testing.T, pool *pgxpool.Pool, box *secretbox.Box, id, table, col, want string) {
	t.Helper()
	var cipher []byte
	if err := pool.QueryRow(context.Background(),
		`SELECT `+col+` FROM `+table+` WHERE id=$1`, id).Scan(&cipher); err != nil {
		t.Fatalf("read %s.%s: %v", table, col, err)
	}
	plain, err := box.Decrypt(cipher)
	if err != nil {
		t.Fatalf("%s.%s decrypt: %v", table, col, err)
	}
	if string(plain) != want {
		t.Fatalf("%s.%s = %q, want %q", table, col, plain, want)
	}
}

func mustExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func rand8() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
