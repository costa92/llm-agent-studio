package studiosvc

import (
	"context"
	"errors"
	"os"
	"testing"

	authzrole "github.com/costa92/llm-agent-authz/role"
	authzstore "github.com/costa92/llm-agent-authz/store"

	"github.com/costa92/llm-agent-studio/internal/storage"
)

// platformFixture opens a gated PG store, migrates authz, ensures the sentinel
// platform org, and returns the Platform service + authz store + a cleanup.
func platformFixture(t *testing.T) (context.Context, *Platform, *authzstore.Store, func()) {
	t.Helper()
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run studiosvc tests")
	}
	ctx := context.Background()
	st, err := storage.Open(ctx, storage.Config{PGURL: dsn})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	az := authzstore.New(st.Pool())
	if err := az.Migrate(ctx); err != nil {
		st.Close()
		t.Fatalf("authz migrate: %v", err)
	}
	p := NewPlatform(az, st.Pool())
	if err := p.EnsureSentinelOrg(ctx); err != nil {
		st.Close()
		t.Fatalf("ensure sentinel org: %v", err)
	}
	return ctx, p, az, st.Close
}

// TestPlatformGrantRevokeRoundTrip proves Grant → IsPlatformAdmin true, Revoke →
// false. The membership lives at (org_id=”, scope_kind='platform').
func TestPlatformGrantRevokeRoundTrip(t *testing.T) {
	ctx, p, az, done := platformFixture(t)
	defer done()
	uid, err := az.CreateUser(ctx, "grant_"+randHexSvc()+"@x.com", "h")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	if ok, err := p.IsPlatformAdmin(ctx, uid); err != nil || ok {
		t.Fatalf("pre-grant want false, got ok=%v err=%v", ok, err)
	}
	if err := p.Grant(ctx, uid); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if ok, err := p.IsPlatformAdmin(ctx, uid); err != nil || !ok {
		t.Fatalf("post-grant want true, got ok=%v err=%v", ok, err)
	}
	// Grant is idempotent.
	if err := p.Grant(ctx, uid); err != nil {
		t.Fatalf("re-grant (idempotent): %v", err)
	}
	if err := p.Revoke(ctx, uid); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if ok, err := p.IsPlatformAdmin(ctx, uid); err != nil || ok {
		t.Fatalf("post-revoke want false, got ok=%v err=%v", ok, err)
	}
}

// TestPlatformGrantByEmail proves found → grants + returns uid; missing → ErrUserNotFound.
func TestPlatformGrantByEmail(t *testing.T) {
	ctx, p, az, done := platformFixture(t)
	defer done()
	email := "byemail_" + randHexSvc() + "@x.com"
	uid, err := az.CreateUser(ctx, email, "h")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	got, err := p.GrantByEmail(ctx, email)
	if err != nil {
		t.Fatalf("grant by email: %v", err)
	}
	if got != uid {
		t.Fatalf("grant by email uid=%q want %q", got, uid)
	}
	if ok, _ := p.IsPlatformAdmin(ctx, uid); !ok {
		t.Fatalf("user not admin after GrantByEmail")
	}

	if _, err := p.GrantByEmail(ctx, "missing_"+randHexSvc()+"@x.com"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("missing email want ErrUserNotFound, got %v", err)
	}
}

// TestPlatformSeedFromEmails proves seeding grants existing users, skips missing
// (no error), and is idempotent.
func TestPlatformSeedFromEmails(t *testing.T) {
	ctx, p, az, done := platformFixture(t)
	defer done()
	e1 := "seed1_" + randHexSvc() + "@x.com"
	uid1, _ := az.CreateUser(ctx, e1, "h")
	missing := "seed_missing_" + randHexSvc() + "@x.com"

	if err := p.SeedFromEmails(ctx, []string{e1, missing, ""}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if ok, _ := p.IsPlatformAdmin(ctx, uid1); !ok {
		t.Fatalf("seeded existing user not admin")
	}
	// Idempotent re-seed.
	if err := p.SeedFromEmails(ctx, []string{e1, missing}); err != nil {
		t.Fatalf("re-seed: %v", err)
	}
}

// TestPlatformListAdmins proves the listing includes granted users (by email order).
func TestPlatformListAdmins(t *testing.T) {
	ctx, p, az, done := platformFixture(t)
	defer done()
	ea := "alist_" + randHexSvc() + "@x.com"
	eb := "blist_" + randHexSvc() + "@x.com"
	ua, _ := az.CreateUser(ctx, ea, "h")
	ub, _ := az.CreateUser(ctx, eb, "h")
	if err := p.Grant(ctx, ua); err != nil {
		t.Fatalf("grant a: %v", err)
	}
	if err := p.Grant(ctx, ub); err != nil {
		t.Fatalf("grant b: %v", err)
	}
	admins, err := p.ListAdmins(ctx)
	if err != nil {
		t.Fatalf("list admins: %v", err)
	}
	seen := map[string]string{}
	for _, a := range admins {
		seen[a.UserID] = a.Email
	}
	if seen[ua] != normalizePlatformEmail(ea) || seen[ub] != normalizePlatformEmail(eb) {
		t.Fatalf("ListAdmins missing granted users: %+v", admins)
	}
}

// TestPlatformListAllOrgs proves all business orgs are returned with member counts,
// and the sentinel platform org (id=”) is excluded.
func TestPlatformListAllOrgs(t *testing.T) {
	ctx, p, az, done := platformFixture(t)
	defer done()
	org := NewOrg(az)
	owner, _ := az.CreateUser(ctx, "owner_"+randHexSvc()+"@x.com", "h")
	member, _ := az.CreateUser(ctx, "member_"+randHexSvc()+"@x.com", "h")

	id1, err := org.CreateOrg(ctx, "OrgOne_"+randHexSvc(), owner)
	if err != nil {
		t.Fatalf("create org1: %v", err)
	}
	id2, err := org.CreateOrg(ctx, "OrgTwo_"+randHexSvc(), owner)
	if err != nil {
		t.Fatalf("create org2: %v", err)
	}
	// Add a second org-level member to id1 → memberCount 2.
	if err := az.UpsertMembership(ctx, id1, member, "org", nil, authzrole.RoleViewer); err != nil {
		t.Fatalf("add member: %v", err)
	}

	orgs, err := p.ListAllOrgs(ctx)
	if err != nil {
		t.Fatalf("list all orgs: %v", err)
	}
	counts := map[string]int64{}
	for _, o := range orgs {
		id, _ := o["id"].(string)
		mc, _ := o["memberCount"].(int64)
		counts[id] = mc
		if id == "" {
			t.Fatalf("sentinel platform org (id='') must be excluded from ListAllOrgs")
		}
	}
	if counts[id1] != 2 {
		t.Fatalf("org1 memberCount=%d want 2", counts[id1])
	}
	if counts[id2] != 1 {
		t.Fatalf("org2 memberCount=%d want 1", counts[id2])
	}
}
