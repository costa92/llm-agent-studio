package studiosvc

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/costa92/llm-agent-authz/password"
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

// TestPlatformListUsers proves ListUsers returns all users with correct
// isPlatformAdmin and orgCount.
func TestPlatformListUsers(t *testing.T) {
	ctx, p, az, done := platformFixture(t)
	defer done()
	org := NewOrg(az)

	adminEmail := "lu_admin_" + randHexSvc() + "@x.com"
	plainEmail := "lu_plain_" + randHexSvc() + "@x.com"
	adminUID, _ := az.CreateUser(ctx, adminEmail, "h")
	plainUID, _ := az.CreateUser(ctx, plainEmail, "h")

	if err := p.Grant(ctx, adminUID); err != nil {
		t.Fatalf("grant: %v", err)
	}
	// adminUID owns two orgs (org_count 2); plainUID belongs to one (org_count 1).
	id1, err := org.CreateOrg(ctx, "LU_OrgOne_"+randHexSvc(), adminUID)
	if err != nil {
		t.Fatalf("create org1: %v", err)
	}
	if _, err := org.CreateOrg(ctx, "LU_OrgTwo_"+randHexSvc(), adminUID); err != nil {
		t.Fatalf("create org2: %v", err)
	}
	if err := az.UpsertMembership(ctx, id1, plainUID, "org", nil, authzrole.RoleViewer); err != nil {
		t.Fatalf("add plain to org1: %v", err)
	}

	users, err := p.ListUsers(ctx)
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	byID := map[string]PlatformUser{}
	for _, u := range users {
		byID[u.UserID] = u
	}
	if got := byID[adminUID]; !got.IsAdmin || got.OrgCount != 2 || got.Email != normalizePlatformEmail(adminEmail) {
		t.Fatalf("admin row wrong: %+v", got)
	}
	if got := byID[plainUID]; got.IsAdmin || got.OrgCount != 1 || got.Email != normalizePlatformEmail(plainEmail) {
		t.Fatalf("plain row wrong: %+v", got)
	}
}

// TestPlatformUserDetail proves UserDetail returns the user's orgs with
// soleOrgAdmin true when they are the only org_admin and false when a second
// org_admin exists; missing id → ErrUserNotFound.
func TestPlatformUserDetail(t *testing.T) {
	ctx, p, az, done := platformFixture(t)
	defer done()
	org := NewOrg(az)

	ownerEmail := "ud_owner_" + randHexSvc() + "@x.com"
	owner, _ := az.CreateUser(ctx, ownerEmail, "h")
	coAdmin, _ := az.CreateUser(ctx, "ud_co_"+randHexSvc()+"@x.com", "h")

	// soloOrg: owner is the only org_admin → soleOrgAdmin true.
	soloOrg, err := org.CreateOrg(ctx, "UD_Solo_"+randHexSvc(), owner)
	if err != nil {
		t.Fatalf("create solo org: %v", err)
	}
	// sharedOrg: owner + coAdmin are both org_admin → soleOrgAdmin false.
	sharedOrg, err := org.CreateOrg(ctx, "UD_Shared_"+randHexSvc(), owner)
	if err != nil {
		t.Fatalf("create shared org: %v", err)
	}
	if err := az.UpsertMembership(ctx, sharedOrg, coAdmin, "org", nil, authzrole.RoleOrgAdmin); err != nil {
		t.Fatalf("add co-admin: %v", err)
	}

	d, err := p.UserDetail(ctx, owner)
	if err != nil {
		t.Fatalf("user detail: %v", err)
	}
	if d.UserID != owner || d.Email != normalizePlatformEmail(ownerEmail) {
		t.Fatalf("detail identity wrong: %+v", d)
	}
	sole := map[string]bool{}
	for _, o := range d.Orgs {
		sole[o.OrgID] = o.SoleOrgAdmin
	}
	if !sole[soloOrg] {
		t.Fatalf("solo org want soleOrgAdmin true: %+v", d.Orgs)
	}
	if sole[sharedOrg] {
		t.Fatalf("shared org want soleOrgAdmin false: %+v", d.Orgs)
	}

	if _, err := p.UserDetail(ctx, "missing_"+randHexSvc()); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("missing user want ErrUserNotFound, got %v", err)
	}
}

// TestPlatformDeleteUser proves DeleteUser SOFT-deletes (issue #23 方案 B): the
// auth_user row survives with deleted_at stamped, the account can no longer log in
// (authz GetUserByEmail filters it) and vanishes from ListUsers, while its
// membership rows are retained for audit. Missing/already-deleted → ErrUserNotFound.
func TestPlatformDeleteUser(t *testing.T) {
	ctx, p, az, done := platformFixture(t)
	defer done()
	org := NewOrg(az)

	email := "del_" + randHexSvc() + "@x.com"
	uid, _ := az.CreateUser(ctx, email, "h")
	if _, err := org.CreateOrg(ctx, "Del_Org_"+randHexSvc(), uid); err != nil {
		t.Fatalf("create org: %v", err)
	}
	if err := p.Grant(ctx, uid); err != nil {
		t.Fatalf("grant: %v", err)
	}

	if err := p.DeleteUser(ctx, uid); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	// Row survives with deleted_at stamped (audit / created-by lineage).
	var n int
	var deletedAt *time.Time
	if err := p.pool.QueryRow(ctx, `SELECT count(*), max(deleted_at) FROM auth_user WHERE id=$1`, uid).Scan(&n, &deletedAt); err != nil {
		t.Fatalf("count user: %v", err)
	}
	if n != 1 || deletedAt == nil {
		t.Fatalf("soft-delete should keep row with deleted_at set: count=%d deleted_at=%v", n, deletedAt)
	}
	// Login lookup no longer finds the user (re-login blocked).
	if _, err := az.GetUserByEmail(ctx, email); !errors.Is(err, authzstore.ErrNotFound) {
		t.Fatalf("GetUserByEmail after soft-delete = %v, want ErrNotFound", err)
	}
	// Excluded from the platform user list.
	users, err := p.ListUsers(ctx)
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	for _, u := range users {
		if u.UserID == uid {
			t.Fatalf("soft-deleted user must not appear in ListUsers")
		}
	}
	// Membership rows retained for audit (NOT cascade-deleted).
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM auth_membership WHERE user_id=$1`, uid).Scan(&n); err != nil {
		t.Fatalf("count memberships: %v", err)
	}
	if n == 0 {
		t.Fatalf("soft-delete should retain membership rows for audit, got 0")
	}

	// Already soft-deleted → ErrUserNotFound.
	if err := p.DeleteUser(ctx, uid); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("double delete want ErrUserNotFound, got %v", err)
	}
	if err := p.DeleteUser(ctx, "missing_"+randHexSvc()); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("missing user want ErrUserNotFound, got %v", err)
	}
}

func TestPlatformResetUserPassword(t *testing.T) {
	ctx, p, az, done := platformFixture(t)
	defer done()

	email := "resetpw_" + randHexSvc() + "@x.com"
	uid, err := az.CreateUser(ctx, email, "old-hash")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Password too short should error
	err = p.ResetUserPassword(ctx, uid, "short")
	if err == nil {
		t.Fatalf("expected error for short password")
	}

	// Valid password reset
	err = p.ResetUserPassword(ctx, uid, "new-strong-password")
	if err != nil {
		t.Fatalf("ResetUserPassword: %v", err)
	}

	// Verify password hash in db is valid
	u, err := az.GetUserByEmail(ctx, email)
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}

	ok, err := password.Verify("new-strong-password", u.PasswordHash)
	if err != nil {
		t.Fatalf("password.Verify error: %v", err)
	}
	if !ok {
		t.Fatalf("password hash verification failed")
	}

	// Test non-existent user
	err = p.ResetUserPassword(ctx, "missing_"+randHexSvc(), "new-strong-password")
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
}

