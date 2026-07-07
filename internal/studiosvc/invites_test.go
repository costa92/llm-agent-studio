package studiosvc

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	authzrole "github.com/costa92/llm-agent-authz/role"
	authzstore "github.com/costa92/llm-agent-authz/store"
	"gorm.io/gorm"

	"github.com/costa92/llm-agent-studio/internal/orginvite"
	"github.com/costa92/llm-agent-studio/internal/storage"
)

// invitesFixture opens a gated PG store, migrates studio + authz, and returns the
// Invites service + its orginvite.Store + the authz store + an Org bootstrapper +
// the gorm handle (for membership assertions) + a cleanup. Mirrors membersFixture.
func invitesFixture(t *testing.T) (context.Context, *Invites, *orginvite.Store, *authzstore.Store, *Org, *gorm.DB, func()) {
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
	// org_invites 表来自 studio m30 迁移；authz 表来自 authz 迁移。二者都要跑。
	if err := st.Migrate(ctx); err != nil {
		st.Close()
		t.Fatalf("studio migrate: %v", err)
	}
	az := authzstore.New(st.Pool())
	if err := az.Migrate(ctx); err != nil {
		st.Close()
		t.Fatalf("authz migrate: %v", err)
	}
	store := orginvite.New(st.GORM())
	inv := NewInvites(store, az, st.GORM())
	return ctx, inv, store, az, NewOrg(az), st.GORM(), st.Close
}

// hasOrgMembership 断言 (org,uid) 存在 org-level membership 并返回其角色。
func orgMembershipRole(t *testing.T, db *gorm.DB, ctx context.Context, orgID, uid string) (string, bool) {
	t.Helper()
	var role string
	err := db.WithContext(ctx).Raw(
		`SELECT role FROM auth_membership WHERE org_id=$1 AND user_id=$2 AND scope_kind='org' AND scope_id IS NULL`,
		orgID, uid).Row().Scan(&role)
	if err != nil {
		return "", false
	}
	return role, true
}

// TestInvitesCreateListRevoke 覆盖创建 → 列出 → 撤销的基本闭环，以及「已是成员 → ErrAlreadyMember」。
func TestInvitesCreateListRevoke(t *testing.T) {
	ctx, inv, _, az, org, _, done := invitesFixture(t)
	defer done()

	owner, _ := az.CreateUser(ctx, "iv_owner_"+randHexSvc()+"@x.com", "h")
	orgID, err := org.CreateOrg(ctx, "IV_Org_"+randHexSvc(), owner)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	inviteEmail := "iv_new_" + randHexSvc() + "@x.com"
	created, err := inv.CreateInvite(ctx, orgID, inviteEmail, authzrole.RoleEditor, owner)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if created.Status != orginvite.StatusPending || created.Token == "" || created.Role != string(authzrole.RoleEditor) {
		t.Fatalf("created invite unexpected: %+v", created)
	}
	if created.Email != normalizePlatformEmail(inviteEmail) {
		t.Fatalf("invite email not normalized: %q", created.Email)
	}

	list, err := inv.ListInvites(ctx, orgID)
	if err != nil {
		t.Fatalf("list invites: %v", err)
	}
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("list want the created invite, got %+v", list)
	}

	// 邀请一个已是成员的邮箱 → ErrAlreadyMember。
	memberEmail := "iv_member_" + randHexSvc() + "@x.com"
	memUID, _ := az.CreateUser(ctx, memberEmail, "h")
	if err := az.UpsertMembership(ctx, orgID, memUID, "org", nil, authzrole.RoleViewer); err != nil {
		t.Fatalf("seed member: %v", err)
	}
	if _, err := inv.CreateInvite(ctx, orgID, memberEmail, authzrole.RoleViewer, owner); !errors.Is(err, ErrAlreadyMember) {
		t.Fatalf("invite existing member want ErrAlreadyMember, got %v", err)
	}

	// 撤销后列表清空。
	if err := inv.RevokeInvite(ctx, orgID, created.ID); err != nil {
		t.Fatalf("revoke invite: %v", err)
	}
	after, _ := inv.ListInvites(ctx, orgID)
	if len(after) != 0 {
		t.Fatalf("after revoke want empty, got %+v", after)
	}
	// 二次撤销（已非 pending）→ ErrNotFound。
	if err := inv.RevokeInvite(ctx, orgID, created.ID); !errors.Is(err, orginvite.ErrNotFound) {
		t.Fatalf("re-revoke want ErrNotFound, got %v", err)
	}
}

// TestInvitesReinviteRefreshes 证明对同一邮箱再次邀请会撤旧发新（列表恒为一封，token 变化）。
func TestInvitesReinviteRefreshes(t *testing.T) {
	ctx, inv, _, az, org, _, done := invitesFixture(t)
	defer done()

	owner, _ := az.CreateUser(ctx, "ir_owner_"+randHexSvc()+"@x.com", "h")
	orgID, _ := org.CreateOrg(ctx, "IR_Org_"+randHexSvc(), owner)

	email := "ir_new_" + randHexSvc() + "@x.com"
	first, err := inv.CreateInvite(ctx, orgID, email, authzrole.RoleViewer, owner)
	if err != nil {
		t.Fatalf("first invite: %v", err)
	}
	second, err := inv.CreateInvite(ctx, orgID, email, authzrole.RoleEditor, owner)
	if err != nil {
		t.Fatalf("re-invite: %v", err)
	}
	if second.Token == first.Token {
		t.Fatalf("re-invite must mint a fresh token")
	}
	list, _ := inv.ListInvites(ctx, orgID)
	if len(list) != 1 || list[0].ID != second.ID {
		t.Fatalf("re-invite want exactly the newest invite, got %+v", list)
	}
	// 旧 token 已被撤销：接受它应因非 pending 被拒。
	uid, _ := az.CreateUser(ctx, email, "h")
	if _, err := inv.AcceptInvite(ctx, first.Token, uid); !errors.Is(err, ErrInviteNotPending) {
		t.Fatalf("accept stale token want ErrInviteNotPending, got %v", err)
	}
}

// TestInvitesAcceptGrantsMembership 证明：匹配邮箱的登录用户接受邀请后，按邀请角色获得
// org membership，邀请转为 accepted；重复接受（已非 pending）被拒。
func TestInvitesAcceptGrantsMembership(t *testing.T) {
	ctx, inv, store, az, org, db, done := invitesFixture(t)
	defer done()

	owner, _ := az.CreateUser(ctx, "ia_owner_"+randHexSvc()+"@x.com", "h")
	orgID, _ := org.CreateOrg(ctx, "IA_Org_"+randHexSvc(), owner)

	email := "ia_invitee_" + randHexSvc() + "@x.com"
	created, err := inv.CreateInvite(ctx, orgID, email, authzrole.RoleEditor, owner)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	// 被邀请人此刻注册/登录（拿到 auth_user + uid）。
	uid, _ := az.CreateUser(ctx, email, "h")

	res, err := inv.AcceptInvite(ctx, created.Token, uid)
	if err != nil {
		t.Fatalf("accept invite: %v", err)
	}
	if res.OrgID != orgID || res.Role != string(authzrole.RoleEditor) {
		t.Fatalf("accept result unexpected: %+v", res)
	}
	role, ok := orgMembershipRole(t, db, ctx, orgID, uid)
	if !ok || role != string(authzrole.RoleEditor) {
		t.Fatalf("membership after accept: ok=%v role=%q want editor", ok, role)
	}
	// 邀请已转 accepted：不再出现在 pending 列表。
	list, _ := inv.ListInvites(ctx, orgID)
	if len(list) != 0 {
		t.Fatalf("accepted invite should leave pending list, got %+v", list)
	}
	// 该行状态确为 accepted 且 accepted_at 非空。
	again, err := store.GetByToken(ctx, created.Token)
	if err != nil {
		t.Fatalf("get by token: %v", err)
	}
	if again.Status != orginvite.StatusAccepted || again.AcceptedAt == nil {
		t.Fatalf("invite not marked accepted: %+v", again)
	}
	// 重复接受 → ErrInviteNotPending。
	if _, err := inv.AcceptInvite(ctx, created.Token, uid); !errors.Is(err, ErrInviteNotPending) {
		t.Fatalf("re-accept want ErrInviteNotPending, got %v", err)
	}
}

// TestInvitesAcceptEmailMismatch 证明：邮箱不符的登录用户接受邀请被拒，且不落任何 membership。
func TestInvitesAcceptEmailMismatch(t *testing.T) {
	ctx, inv, _, az, org, db, done := invitesFixture(t)
	defer done()

	owner, _ := az.CreateUser(ctx, "im_owner_"+randHexSvc()+"@x.com", "h")
	orgID, _ := org.CreateOrg(ctx, "IM_Org_"+randHexSvc(), owner)

	created, err := inv.CreateInvite(ctx, orgID, "im_target_"+randHexSvc()+"@x.com", authzrole.RoleViewer, owner)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	// 另一个登录用户（邮箱不同）尝试接受。
	otherUID, _ := az.CreateUser(ctx, "im_other_"+randHexSvc()+"@x.com", "h")
	if _, err := inv.AcceptInvite(ctx, created.Token, otherUID); !errors.Is(err, ErrInviteEmailMismatch) {
		t.Fatalf("mismatch accept want ErrInviteEmailMismatch, got %v", err)
	}
	if _, ok := orgMembershipRole(t, db, ctx, orgID, otherUID); ok {
		t.Fatalf("mismatch accept must not create membership")
	}
	// 邀请仍 pending。
	list, _ := inv.ListInvites(ctx, orgID)
	if len(list) != 1 {
		t.Fatalf("invite should remain pending after mismatch, got %+v", list)
	}
}

// TestInvitesAcceptExpired 证明：已过期的邀请接受被拒（ErrInviteExpired），不落 membership。
// 用 store 直接种一封 ttl 为负的邀请（svc.CreateInvite 恒用固定 TTL，无法造过期）。
func TestInvitesAcceptExpired(t *testing.T) {
	ctx, inv, store, az, org, db, done := invitesFixture(t)
	defer done()

	owner, _ := az.CreateUser(ctx, "ie_owner_"+randHexSvc()+"@x.com", "h")
	orgID, _ := org.CreateOrg(ctx, "IE_Org_"+randHexSvc(), owner)

	email := "ie_invitee_" + randHexSvc() + "@x.com"
	uid, _ := az.CreateUser(ctx, email, "h")
	expired, err := store.Create(ctx, orgID, normalizePlatformEmail(email), string(authzrole.RoleViewer), owner, -time.Hour)
	if err != nil {
		t.Fatalf("seed expired invite: %v", err)
	}
	if _, err := inv.AcceptInvite(ctx, expired.Token, uid); !errors.Is(err, ErrInviteExpired) {
		t.Fatalf("expired accept want ErrInviteExpired, got %v", err)
	}
	if _, ok := orgMembershipRole(t, db, ctx, orgID, uid); ok {
		t.Fatalf("expired accept must not create membership")
	}
}

// TestInvitesAcceptUnknownToken 证明：未知 token → orginvite.ErrNotFound。
func TestInvitesAcceptUnknownToken(t *testing.T) {
	ctx, inv, _, az, _, _, done := invitesFixture(t)
	defer done()

	uid, _ := az.CreateUser(ctx, "iu_"+randHexSvc()+"@x.com", "h")
	if _, err := inv.AcceptInvite(ctx, "deadbeef"+randHexSvc(), uid); !errors.Is(err, orginvite.ErrNotFound) {
		t.Fatalf("unknown token want ErrNotFound, got %v", err)
	}
}
