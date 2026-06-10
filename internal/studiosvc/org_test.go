package studiosvc

import (
	"context"
	"os"
	"testing"

	authzstore "github.com/costa92/llm-agent-authz/store"

	"github.com/costa92/llm-agent-studio/internal/storage"
)

func TestCreateOrgGrantsOrgAdmin(t *testing.T) {
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
	uid, err := az.CreateUser(ctx, "svc_"+randHexSvc()+"@x.com", "hash")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	o := NewOrg(az)
	orgID, err := o.CreateOrg(ctx, "Acme", uid)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	r, err := az.ResolveRole(ctx, uid, orgID, "org", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.Rank() < 4 { // org_admin = rank 4
		t.Fatalf("creator role=%q want org_admin", r)
	}
}
