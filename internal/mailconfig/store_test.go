package mailconfig

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"testing"

	"github.com/costa92/llm-agent-studio/internal/secretbox"
	"github.com/costa92/llm-agent-studio/internal/storage"
)

func TestMailConfigStore(t *testing.T) {
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run mailconfig tests")
	}
	ctx := context.Background()
	st, err := storage.Open(ctx, storage.Config{PGURL: dsn})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Create a secretbox master key
	keyBytes := make([]byte, 32)
	_, _ = rand.Read(keyBytes)
	keyB64 := base64.StdEncoding.EncodeToString(keyBytes)
	box, err := secretbox.New(keyB64)
	if err != nil {
		t.Fatalf("new box: %v", err)
	}

	store := New(st.GORM(), box)

	// Clean up previous global config to ensure deterministic test
	_, _ = st.Pool().Exec(ctx, "DELETE FROM mail_configs WHERE scope='global'")

	// 1. Get when empty
	_, err = store.GetGlobal(ctx)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	// 2. Upsert config
	in := UpsertInput{
		SMTPHost: "smtp.test.com",
		SMTPPort: 465,
		SMTPUser: "user@test.com",
		SMTPPass: "super_secret_password",
		SMTPFrom: "from@test.com",
		Enabled:  true,
	}

	err = store.UpsertGlobal(ctx, in)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// 3. Get config and check values (password must be hidden/masked, HasSecret must be true)
	cfg, err := store.GetGlobal(ctx)
	if err != nil {
		t.Fatalf("get global: %v", err)
	}
	if cfg.SMTPHost != in.SMTPHost || cfg.SMTPPort != in.SMTPPort || cfg.SMTPUser != in.SMTPUser || cfg.SMTPFrom != in.SMTPFrom || !cfg.Enabled || !cfg.HasSecret {
		t.Fatalf("unexpected get global config fields: %+v", cfg)
	}

	// 4. Resolve global and verify password is decrypted correctly
	res, err := store.ResolveGlobal(ctx)
	if err != nil {
		t.Fatalf("resolve global: %v", err)
	}
	if res.SMTPHost != in.SMTPHost || res.SMTPPort != in.SMTPPort || res.SMTPUser != in.SMTPUser || res.SMTPFrom != in.SMTPFrom || !res.Enabled || res.SMTPPass != in.SMTPPass {
		t.Fatalf("unexpected resolved global config fields: %+v", res)
	}

	// 5. Update without password change (pass is empty)
	in2 := UpsertInput{
		SMTPHost: "smtp.test2.com",
		SMTPPort: 587,
		SMTPUser: "user2@test.com",
		SMTPFrom: "from2@test.com",
		Enabled:  false,
	}
	err = store.UpsertGlobal(ctx, in2)
	if err != nil {
		t.Fatalf("upsert 2: %v", err)
	}

	res2, err := store.ResolveGlobal(ctx)
	if err != nil {
		t.Fatalf("resolve global 2: %v", err)
	}
	// Password should retain the old value
	if res2.SMTPPass != in.SMTPPass {
		t.Fatalf("expected password to remain %q, got %q", in.SMTPPass, res2.SMTPPass)
	}
	if res2.SMTPHost != in2.SMTPHost || res2.SMTPPort != in2.SMTPPort || res2.SMTPUser != in2.SMTPUser || res2.SMTPFrom != in2.SMTPFrom || res2.Enabled {
		t.Fatalf("unexpected fields after update: %+v", res2)
	}
}
