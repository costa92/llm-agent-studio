package studiosvc

import (
	"context"
	"errors"
	"os"
	"testing"

	authzstore "github.com/costa92/llm-agent-authz/store"

	"github.com/costa92/llm-agent-studio/internal/storage"
)

func TestRegisterCreateDuplicateEmail(t *testing.T) {
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run studiosvc tests")
	}
	ctx := context.Background()
	st, err := storage.Open(ctx, storage.Config{PGURL: dsn})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	az := authzstore.New(st.Pool())
	if err := az.Migrate(ctx); err != nil {
		t.Fatalf("authz migrate: %v", err)
	}
	r := NewRegister(az)
	email := "reg_" + randHexSvc() + "@x.com"

	uid, err := r.Create(ctx, email, "password123")
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	if uid == "" {
		t.Fatalf("first create returned empty user id")
	}

	// Same email again → ErrEmailExists.
	if _, err := r.Create(ctx, email, "password123"); !errors.Is(err, ErrEmailExists) {
		t.Fatalf("duplicate create err = %v, want ErrEmailExists", err)
	}
}
