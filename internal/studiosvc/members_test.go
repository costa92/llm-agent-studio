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

// membersFixture opens a gated PG store, migrates authz, and returns the Members
// service + authz store + an Org bootstrapper + a cleanup. Mirrors platformFixture.
func membersFixture(t *testing.T) (context.Context, *Members, *authzstore.Store, *Org, func()) {
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
	m := NewMembers(az, st.GORM())
	return ctx, m, az, NewOrg(az), st.Close
}

// TestMembersListMembers proves ListMembers returns the creator (org_admin) plus
// added members, ordered by email, and an empty (non-nil) slice for an org with
// no members.
func TestMembersListMembers(t *testing.T) {
	ctx, m, az, org, done := membersFixture(t)
	defer done()

	ownerEmail := "ml_owner_" + randHexSvc() + "@x.com"
	owner, _ := az.CreateUser(ctx, ownerEmail, "h")
	orgID, err := org.CreateOrg(ctx, "ML_Org_"+randHexSvc(), owner)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	memberEmail := "ml_member_" + randHexSvc() + "@x.com"
	if _, err := az.CreateUser(ctx, memberEmail, "h"); err != nil {
		t.Fatalf("create member user: %v", err)
	}
	if _, err := m.AddMemberByEmail(ctx, orgID, memberEmail, authzrole.RoleViewer); err != nil {
		t.Fatalf("add member: %v", err)
	}

	got, err := m.ListMembers(ctx, orgID)
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	byEmail := map[string]OrgMember{}
	for _, mem := range got {
		byEmail[mem.Email] = mem
	}
	if r := byEmail[normalizePlatformEmail(ownerEmail)]; r.Role != string(authzrole.RoleOrgAdmin) {
		t.Fatalf("owner role=%q want org_admin: %+v", r.Role, got)
	}
	if r := byEmail[normalizePlatformEmail(memberEmail)]; r.Role != string(authzrole.RoleViewer) {
		t.Fatalf("member role=%q want viewer: %+v", r.Role, got)
	}

	// Empty org → non-nil empty slice.
	empty, err := m.ListMembers(ctx, "missing-org-"+randHexSvc())
	if err != nil {
		t.Fatalf("list empty: %v", err)
	}
	if empty == nil {
		t.Fatalf("ListMembers must return non-nil empty slice")
	}
	if len(empty) != 0 {
		t.Fatalf("missing org want 0 members, got %d", len(empty))
	}
}

// TestMembersAddMemberByEmail proves a new member is added with the requested
// role, an unknown email → ErrUserNotFound, and re-adding with a different role
// updates it (idempotent upsert).
func TestMembersAddMemberByEmail(t *testing.T) {
	ctx, m, az, org, done := membersFixture(t)
	defer done()

	owner, _ := az.CreateUser(ctx, "ma_owner_"+randHexSvc()+"@x.com", "h")
	orgID, err := org.CreateOrg(ctx, "MA_Org_"+randHexSvc(), owner)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	email := "ma_new_" + randHexSvc() + "@x.com"
	uid, _ := az.CreateUser(ctx, email, "h")
	got, err := m.AddMemberByEmail(ctx, orgID, email, authzrole.RoleViewer)
	if err != nil {
		t.Fatalf("add member: %v", err)
	}
	if got.UserID != uid || got.Email != normalizePlatformEmail(email) || got.Role != string(authzrole.RoleViewer) {
		t.Fatalf("added member wrong: %+v", got)
	}

	// Unknown email → ErrUserNotFound.
	if _, err := m.AddMemberByEmail(ctx, orgID, "ma_missing_"+randHexSvc()+"@x.com", authzrole.RoleViewer); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("unknown email want ErrUserNotFound, got %v", err)
	}

	// Re-add with a different role → role updated (idempotent upsert).
	if _, err := m.AddMemberByEmail(ctx, orgID, email, authzrole.RoleEditor); err != nil {
		t.Fatalf("re-add member: %v", err)
	}
	r, ok, err := m.currentOrgRole(ctx, orgID, uid)
	if err != nil || !ok {
		t.Fatalf("current role lookup: ok=%v err=%v", ok, err)
	}
	if r != authzrole.RoleEditor {
		t.Fatalf("re-add role=%q want editor", r)
	}
}

// TestMembersSetMemberRole proves: demoting the sole org_admin → ErrLastOrgAdmin;
// a non-member → ErrMemberNotFound; demotion succeeds once a 2nd org_admin exists.
func TestMembersSetMemberRole(t *testing.T) {
	ctx, m, az, org, done := membersFixture(t)
	defer done()

	owner, _ := az.CreateUser(ctx, "sr_owner_"+randHexSvc()+"@x.com", "h")
	orgID, err := org.CreateOrg(ctx, "SR_Org_"+randHexSvc(), owner)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	// Demote the sole org_admin → ErrLastOrgAdmin.
	if err := m.SetMemberRole(ctx, orgID, owner, authzrole.RoleViewer); !errors.Is(err, ErrLastOrgAdmin) {
		t.Fatalf("demote sole org_admin want ErrLastOrgAdmin, got %v", err)
	}

	// Non-member → ErrMemberNotFound.
	stranger, _ := az.CreateUser(ctx, "sr_stranger_"+randHexSvc()+"@x.com", "h")
	if err := m.SetMemberRole(ctx, orgID, stranger, authzrole.RoleEditor); !errors.Is(err, ErrMemberNotFound) {
		t.Fatalf("non-member want ErrMemberNotFound, got %v", err)
	}

	// Add a 2nd org_admin → now demoting the owner is allowed.
	coEmail := "sr_co_" + randHexSvc() + "@x.com"
	co, _ := az.CreateUser(ctx, coEmail, "h")
	if _, err := m.AddMemberByEmail(ctx, orgID, coEmail, authzrole.RoleOrgAdmin); err != nil {
		t.Fatalf("add co-admin: %v", err)
	}
	if err := m.SetMemberRole(ctx, orgID, owner, authzrole.RoleViewer); err != nil {
		t.Fatalf("demote with 2nd admin present: %v", err)
	}
	r, _, _ := m.currentOrgRole(ctx, orgID, owner)
	if r != authzrole.RoleViewer {
		t.Fatalf("owner role after demote=%q want viewer", r)
	}
	_ = co
}

// TestMembersRemoveMember proves: removing the sole org_admin → ErrLastOrgAdmin;
// a non-member → ErrMemberNotFound; removing a normal member succeeds; removing
// an org_admin succeeds once a 2nd org_admin exists.
func TestMembersRemoveMember(t *testing.T) {
	ctx, m, az, org, done := membersFixture(t)
	defer done()

	owner, _ := az.CreateUser(ctx, "rm_owner_"+randHexSvc()+"@x.com", "h")
	orgID, err := org.CreateOrg(ctx, "RM_Org_"+randHexSvc(), owner)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	// Remove the sole org_admin → ErrLastOrgAdmin.
	if err := m.RemoveMember(ctx, orgID, owner); !errors.Is(err, ErrLastOrgAdmin) {
		t.Fatalf("remove sole org_admin want ErrLastOrgAdmin, got %v", err)
	}

	// Non-member → ErrMemberNotFound.
	stranger, _ := az.CreateUser(ctx, "rm_stranger_"+randHexSvc()+"@x.com", "h")
	if err := m.RemoveMember(ctx, orgID, stranger); !errors.Is(err, ErrMemberNotFound) {
		t.Fatalf("non-member want ErrMemberNotFound, got %v", err)
	}

	// Add a normal member, then remove → succeeds.
	memEmail := "rm_member_" + randHexSvc() + "@x.com"
	mem, _ := az.CreateUser(ctx, memEmail, "h")
	if _, err := m.AddMemberByEmail(ctx, orgID, memEmail, authzrole.RoleViewer); err != nil {
		t.Fatalf("add member: %v", err)
	}
	if err := m.RemoveMember(ctx, orgID, mem); err != nil {
		t.Fatalf("remove normal member: %v", err)
	}
	if _, ok, _ := m.currentOrgRole(ctx, orgID, mem); ok {
		t.Fatalf("member still present after remove")
	}

	// Add a 2nd org_admin, then removing the owner is allowed.
	coEmail := "rm_co_" + randHexSvc() + "@x.com"
	co, _ := az.CreateUser(ctx, coEmail, "h")
	if _, err := m.AddMemberByEmail(ctx, orgID, coEmail, authzrole.RoleOrgAdmin); err != nil {
		t.Fatalf("add co-admin: %v", err)
	}
	if err := m.RemoveMember(ctx, orgID, owner); err != nil {
		t.Fatalf("remove org_admin with 2nd admin present: %v", err)
	}
	_ = co
}
