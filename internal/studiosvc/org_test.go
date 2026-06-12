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

func TestOrgsForUserListsMemberships(t *testing.T) {
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
	uid, err := az.CreateUser(ctx, "list_"+randHexSvc()+"@x.com", "hash")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	o := NewOrg(az)
	id1, err := o.CreateOrg(ctx, "Zeta", uid)
	if err != nil {
		t.Fatalf("create org1: %v", err)
	}
	id2, err := o.CreateOrg(ctx, "Alpha", uid)
	if err != nil {
		t.Fatalf("create org2: %v", err)
	}
	// A second user's org must NOT leak into this user's list.
	other, _ := az.CreateUser(ctx, "other_"+randHexSvc()+"@x.com", "hash")
	if _, err := o.CreateOrg(ctx, "NotMine", other); err != nil {
		t.Fatalf("create other org: %v", err)
	}

	got, err := NewOrgList(st.Pool()).OrgsForUser(ctx, uid)
	if err != nil {
		t.Fatalf("OrgsForUser: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d orgs, want 2: %+v", len(got), got)
	}
	// ordered by name → Alpha then Zeta
	if got[0]["name"] != "Alpha" || got[0]["id"] != id2 {
		t.Fatalf("first = %+v, want Alpha/%s", got[0], id2)
	}
	if got[1]["name"] != "Zeta" || got[1]["id"] != id1 {
		t.Fatalf("second = %+v, want Zeta/%s", got[1], id1)
	}
	if got[0]["role"] == "" || got[0]["role"] == nil {
		t.Fatalf("missing role: %+v", got[0])
	}
}
