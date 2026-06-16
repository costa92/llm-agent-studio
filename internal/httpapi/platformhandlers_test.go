package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	authzrole "github.com/costa92/llm-agent-authz/role"
	authztoken "github.com/costa92/llm-agent-authz/token"

	"github.com/costa92/llm-agent-studio/internal/studiosvc"
)

// stubPlatform 是 PlatformService 的假实现，记录最近一次入参，结果可注入。
type stubPlatform struct {
	isAdmin    bool
	admins     []studiosvc.PlatformAdmin
	allOrgs    []map[string]any
	grantUID   string
	grantErr   error
	lastGrant  string
	lastRevoke string
	revokeErr  error

	users      []studiosvc.PlatformUser
	detail     studiosvc.UserDetail
	detailErr  error
	deleteErr  error
	lastDelete string

	lastResetUID string
	lastResetPwd string
	resetErr     error
}

func (s *stubPlatform) IsPlatformAdmin(context.Context, string) (bool, error) { return s.isAdmin, nil }
func (s *stubPlatform) ListAdmins(context.Context) ([]studiosvc.PlatformAdmin, error) {
	return s.admins, nil
}
func (s *stubPlatform) GrantByEmail(_ context.Context, email string) (string, error) {
	s.lastGrant = email
	return s.grantUID, s.grantErr
}
func (s *stubPlatform) Revoke(_ context.Context, userID string) error {
	s.lastRevoke = userID
	return s.revokeErr
}
func (s *stubPlatform) ListAllOrgs(context.Context) ([]map[string]any, error) { return s.allOrgs, nil }
func (s *stubPlatform) ListUsers(context.Context) ([]studiosvc.PlatformUser, error) {
	return s.users, nil
}
func (s *stubPlatform) UserDetail(_ context.Context, userID string) (studiosvc.UserDetail, error) {
	return s.detail, s.detailErr
}
func (s *stubPlatform) DeleteUser(_ context.Context, userID string) error {
	s.lastDelete = userID
	return s.deleteErr
}

func (s *stubPlatform) ResetUserPassword(_ context.Context, userID, newPassword string) error {
	s.lastResetUID = userID
	s.lastResetPwd = newPassword
	return s.resetErr
}

// platformResolver is a fake RoleResolver: returns admin on scope_kind="platform"
// for adminUID only; RoleNone otherwise. Lets the mux's RequireScopeRole gate run.
type platformResolver struct{ adminUID string }

func (r platformResolver) ResolveRole(_ context.Context, uid, _, scopeKind, _ string) (authzrole.Role, error) {
	if scopeKind == "platform" && uid == r.adminUID {
		return authzrole.RoleAdmin, nil
	}
	return authzrole.RoleNone, nil
}

// TestPlatformWhoami proves whoami returns the bool, gated by authOnly (no 403).
func TestPlatformWhoami(t *testing.T) {
	rr := httptest.NewRecorder()
	platformWhoamiHandler(&stubPlatform{isAdmin: true})(rr, httptest.NewRequest("GET", "/api/platform/whoami", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"isPlatformAdmin":true`) {
		t.Fatalf("whoami admin want true: %d %s", rr.Code, rr.Body.String())
	}
	rr2 := httptest.NewRecorder()
	platformWhoamiHandler(&stubPlatform{isAdmin: false})(rr2, httptest.NewRequest("GET", "/api/platform/whoami", nil))
	if !strings.Contains(rr2.Body.String(), `"isPlatformAdmin":false`) {
		t.Fatalf("whoami non-admin want false: %s", rr2.Body.String())
	}
}

// TestPlatformGrantAdmin proves POST happy path → 200 {userId} and ErrUserNotFound → 404.
func TestPlatformGrantAdmin(t *testing.T) {
	st := &stubPlatform{grantUID: "u-123"}
	rr := httptest.NewRecorder()
	platformGrantAdminHandler(st)(rr, storageReq("POST", "/api/platform/admins", `{"email":"a@x.com"}`))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"userId":"u-123"`) {
		t.Fatalf("grant want 200 {userId}: %d %s", rr.Code, rr.Body.String())
	}
	if st.lastGrant != "a@x.com" {
		t.Fatalf("grant email not passed: %q", st.lastGrant)
	}

	rr2 := httptest.NewRecorder()
	platformGrantAdminHandler(&stubPlatform{grantErr: studiosvc.ErrUserNotFound})(rr2,
		storageReq("POST", "/api/platform/admins", `{"email":"nobody@x.com"}`))
	if rr2.Code != http.StatusNotFound {
		t.Fatalf("grant unknown email want 404, got %d", rr2.Code)
	}

	rr3 := httptest.NewRecorder()
	platformGrantAdminHandler(st)(rr3, storageReq("POST", "/api/platform/admins", `{}`))
	if rr3.Code != http.StatusBadRequest {
		t.Fatalf("grant missing email want 400, got %d", rr3.Code)
	}
}

// TestPlatformRevokeAdmin proves DELETE happy path → 200 {ok:true} and the
// last-admin guard → 409 (refuses to remove the sole platform admin).
func TestPlatformRevokeAdmin(t *testing.T) {
	// Two admins → revoking one is allowed.
	st := &stubPlatform{admins: []studiosvc.PlatformAdmin{{UserID: "u1", Email: "a@x.com"}, {UserID: "u2", Email: "b@x.com"}}}
	rr := httptest.NewRecorder()
	req := storageReq("DELETE", "/api/platform/admins/u1", "")
	req.SetPathValue("userId", "u1")
	platformRevokeAdminHandler(st)(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"ok":true`) {
		t.Fatalf("revoke want 200 {ok:true}: %d %s", rr.Code, rr.Body.String())
	}
	if st.lastRevoke != "u1" {
		t.Fatalf("revoke target not passed: %q", st.lastRevoke)
	}

	// Sole admin → revoking that same user is refused (409). Also covers self-revoke
	// when the caller is the only admin.
	stLast := &stubPlatform{admins: []studiosvc.PlatformAdmin{{UserID: "only", Email: "o@x.com"}}}
	rr2 := httptest.NewRecorder()
	req2 := storageReq("DELETE", "/api/platform/admins/only", "")
	req2.SetPathValue("userId", "only")
	platformRevokeAdminHandler(stLast)(rr2, req2)
	if rr2.Code != http.StatusConflict {
		t.Fatalf("revoke last admin want 409, got %d", rr2.Code)
	}
	if stLast.lastRevoke != "" {
		t.Fatalf("last-admin revoke must not call Revoke, got target %q", stLast.lastRevoke)
	}
}

// TestPlatformOrgsAndAdminsHandlers proves the list handlers wrap results in {items}.
func TestPlatformOrgsAndAdminsHandlers(t *testing.T) {
	st := &stubPlatform{
		allOrgs: []map[string]any{{"id": "o1", "name": "Acme", "memberCount": int64(3)}},
		admins:  []studiosvc.PlatformAdmin{{UserID: "u1", Email: "a@x.com"}},
	}
	rr := httptest.NewRecorder()
	platformOrgsHandler(st)(rr, httptest.NewRequest("GET", "/api/platform/orgs", nil))
	if !strings.Contains(rr.Body.String(), `"memberCount":3`) || !strings.Contains(rr.Body.String(), `"items"`) {
		t.Fatalf("orgs body: %s", rr.Body.String())
	}
	rr2 := httptest.NewRecorder()
	platformListAdminsHandler(st)(rr2, httptest.NewRequest("GET", "/api/platform/admins", nil))
	if !strings.Contains(rr2.Body.String(), `"email":"a@x.com"`) {
		t.Fatalf("admins body: %s", rr2.Body.String())
	}
}

// TestPlatformListUsersHandler proves GET wraps users in {items} (nil → []).
func TestPlatformListUsersHandler(t *testing.T) {
	st := &stubPlatform{users: []studiosvc.PlatformUser{{UserID: "u1", Email: "a@x.com", IsAdmin: true, OrgCount: 2}}}
	rr := httptest.NewRecorder()
	platformListUsersHandler(st)(rr, httptest.NewRequest("GET", "/api/platform/users", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"items"`) ||
		!strings.Contains(rr.Body.String(), `"isPlatformAdmin":true`) || !strings.Contains(rr.Body.String(), `"orgCount":2`) {
		t.Fatalf("list users body: %d %s", rr.Code, rr.Body.String())
	}
	// nil → [] (not null).
	rr2 := httptest.NewRecorder()
	platformListUsersHandler(&stubPlatform{})(rr2, httptest.NewRequest("GET", "/api/platform/users", nil))
	if !strings.Contains(rr2.Body.String(), `"items":[]`) {
		t.Fatalf("nil users want items:[], got %s", rr2.Body.String())
	}
}

// TestPlatformUserDetailHandler proves GET → 200 detail object, missing → 404,
// empty pathvalue → 400.
func TestPlatformUserDetailHandler(t *testing.T) {
	st := &stubPlatform{detail: studiosvc.UserDetail{
		UserID: "u1", Email: "a@x.com", IsAdmin: true,
		Orgs: []studiosvc.UserOrgMembership{{OrgID: "o1", OrgName: "Acme", Role: "org_admin", SoleOrgAdmin: true}},
	}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/platform/users/u1", nil)
	req.SetPathValue("userId", "u1")
	platformUserDetailHandler(st)(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"userId":"u1"`) ||
		!strings.Contains(rr.Body.String(), `"soleOrgAdmin":true`) {
		t.Fatalf("detail body: %d %s", rr.Code, rr.Body.String())
	}

	// Missing user → 404.
	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/api/platform/users/nope", nil)
	req2.SetPathValue("userId", "nope")
	platformUserDetailHandler(&stubPlatform{detailErr: studiosvc.ErrUserNotFound})(rr2, req2)
	if rr2.Code != http.StatusNotFound {
		t.Fatalf("missing user want 404, got %d", rr2.Code)
	}

	// Empty pathvalue → 400.
	rr3 := httptest.NewRecorder()
	platformUserDetailHandler(st)(rr3, httptest.NewRequest("GET", "/api/platform/users/", nil))
	if rr3.Code != http.StatusBadRequest {
		t.Fatalf("empty userId want 400, got %d", rr3.Code)
	}
}

// TestPlatformDeleteUserHandler proves DELETE happy → 200 {ok:true}, missing → 404,
// last-platform-admin guard → 409.
func TestPlatformDeleteUserHandler(t *testing.T) {
	// Happy path: target is not an admin → deleted.
	st := &stubPlatform{admins: []studiosvc.PlatformAdmin{{UserID: "admin1", Email: "x@x.com"}}}
	rr := httptest.NewRecorder()
	req := storageReq("DELETE", "/api/platform/users/u-target", "")
	req.SetPathValue("userId", "u-target")
	platformDeleteUserHandler(st)(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"ok":true`) {
		t.Fatalf("delete want 200 {ok:true}: %d %s", rr.Code, rr.Body.String())
	}
	if st.lastDelete != "u-target" {
		t.Fatalf("delete target not passed: %q", st.lastDelete)
	}

	// Missing user → 404.
	rr2 := httptest.NewRecorder()
	req2 := storageReq("DELETE", "/api/platform/users/gone", "")
	req2.SetPathValue("userId", "gone")
	platformDeleteUserHandler(&stubPlatform{deleteErr: studiosvc.ErrUserNotFound})(rr2, req2)
	if rr2.Code != http.StatusNotFound {
		t.Fatalf("missing user want 404, got %d", rr2.Code)
	}

	// Empty pathvalue → 400.
	rr3 := httptest.NewRecorder()
	platformDeleteUserHandler(st)(rr3, storageReq("DELETE", "/api/platform/users/", ""))
	if rr3.Code != http.StatusBadRequest {
		t.Fatalf("empty userId want 400, got %d", rr3.Code)
	}

	// Last-platform-admin guard → 409 (target is the sole admin).
	stLast := &stubPlatform{admins: []studiosvc.PlatformAdmin{{UserID: "only", Email: "o@x.com"}}}
	rr4 := httptest.NewRecorder()
	req4 := storageReq("DELETE", "/api/platform/users/only", "")
	req4.SetPathValue("userId", "only")
	platformDeleteUserHandler(stLast)(rr4, req4)
	if rr4.Code != http.StatusConflict {
		t.Fatalf("delete last admin want 409, got %d", rr4.Code)
	}
	if stLast.lastDelete != "" {
		t.Fatalf("last-admin delete must not call DeleteUser, got %q", stLast.lastDelete)
	}
}

// TestPlatformDeleteSelfHandler proves a platform admin cannot delete themselves
// → 409. Routed through the full mux so the caller UserID is populated from the
// token (the handler compares target against authzhttp.UserID).
func TestPlatformDeleteSelfHandler(t *testing.T) {
	iss := authztoken.NewIssuer([]byte("plat-secret"), time.Minute)
	// Two admins so the last-admin guard does NOT fire — isolate the self guard.
	st := &stubPlatform{admins: []studiosvc.PlatformAdmin{{UserID: "u-admin", Email: "a@x.com"}, {UserID: "u-other", Email: "b@x.com"}}}
	mux := NewMux(Deps{
		Issuer: iss, RoleResolver: platformResolver{adminUID: "u-admin"},
		Platform: st,
	})
	req := httptest.NewRequest("DELETE", "/api/platform/users/u-admin", nil)
	req.Header.Set("Authorization", "Bearer "+mintToken(t, iss, "u-admin"))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("delete self want 409, got %d body=%s", rr.Code, rr.Body.String())
	}
	if st.lastDelete != "" {
		t.Fatalf("self-delete must not call DeleteUser, got %q", st.lastDelete)
	}
}

// TestPlatformGateRouting proves the platform-gated routes 403 for a non-platform
// user and admit a platform admin — routed through the full mux so the real
// Authenticate→RequireScopeRole(platform) chain runs. whoami is NOT gated.
func TestPlatformGateRouting(t *testing.T) {
	iss := authztoken.NewIssuer([]byte("plat-secret"), time.Minute)
	mux := NewMux(Deps{
		Issuer: iss, RoleResolver: platformResolver{adminUID: "u-admin"},
		Platform:      &stubPlatform{allOrgs: []map[string]any{}},
		StorageConfig: &stubSCStore{},
	})

	// Non-platform user → 403 on gated routes (orgs + the new users route).
	for _, path := range []string{"/api/platform/orgs", "/api/platform/users"} {
		reqNo := httptest.NewRequest("GET", path, nil)
		reqNo.Header.Set("Authorization", "Bearer "+mintToken(t, iss, "u-plain"))
		rrNo := httptest.NewRecorder()
		mux.ServeHTTP(rrNo, reqNo)
		if rrNo.Code != http.StatusForbidden {
			t.Fatalf("non-platform %s want 403, got %d body=%s", path, rrNo.Code, rrNo.Body.String())
		}
	}

	// Platform admin → admitted (200).
	reqYes := httptest.NewRequest("GET", "/api/platform/orgs", nil)
	reqYes.Header.Set("Authorization", "Bearer "+mintToken(t, iss, "u-admin"))
	rrYes := httptest.NewRecorder()
	mux.ServeHTTP(rrYes, reqYes)
	if rrYes.Code != http.StatusOK {
		t.Fatalf("platform admin want 200, got %d body=%s", rrYes.Code, rrYes.Body.String())
	}

	// whoami is authOnly (not gated): a non-platform user gets 200 {isPlatformAdmin:false-ish}.
	muxWho := NewMux(Deps{
		Issuer: iss, RoleResolver: platformResolver{adminUID: "u-admin"},
		Platform: &stubPlatform{isAdmin: false},
	})
	reqWho := httptest.NewRequest("GET", "/api/platform/whoami", nil)
	reqWho.Header.Set("Authorization", "Bearer "+mintToken(t, iss, "u-plain"))
	rrWho := httptest.NewRecorder()
	muxWho.ServeHTTP(rrWho, reqWho)
	if rrWho.Code != http.StatusOK {
		t.Fatalf("whoami must not be gated, want 200, got %d", rrWho.Code)
	}
}

func TestPlatformResetPasswordHandler(t *testing.T) {
	t.Run("成功：admin 重置任意用户密码", func(t *testing.T) {
		st := &stubPlatform{}
		rr := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/platform/users/u-target/reset-password",
			strings.NewReader(`{"newPassword":"newSecure1234"}`))
		req.SetPathValue("userId", "u-target")
		platformResetPasswordHandler(st)(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("want 200, got %d body=%s", rr.Code, rr.Body.String())
		}
		if st.lastResetUID != "u-target" {
			t.Fatalf("target=%q want u-target", st.lastResetUID)
		}
		if st.lastResetPwd != "newSecure1234" {
			t.Fatalf("password not forwarded: %q", st.lastResetPwd)
		}
		if !strings.Contains(rr.Body.String(), `"ok":true`) {
			t.Fatalf("response missing ok:true: %s", rr.Body.String())
		}
	})
	t.Run("400 缺 newPassword", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/platform/users/u/reset-password",
			strings.NewReader(`{}`))
		req.SetPathValue("userId", "u")
		platformResetPasswordHandler(&stubPlatform{})(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("want 400, got %d", rr.Code)
		}
	})
	t.Run("400 invalid body", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/platform/users/u/reset-password",
			strings.NewReader(`not-json`))
		req.SetPathValue("userId", "u")
		platformResetPasswordHandler(&stubPlatform{})(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("want 400, got %d", rr.Code)
		}
	})
	t.Run("404 user 不存在", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/platform/users/ghost/reset-password",
			strings.NewReader(`{"newPassword":"newSecure1234"}`))
		req.SetPathValue("userId", "ghost")
		platformResetPasswordHandler(&stubPlatform{resetErr: studiosvc.ErrUserNotFound})(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("want 404, got %d", rr.Code)
		}
	})
	t.Run("400 密码弱（studiosvc: password 前缀）", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/platform/users/u/reset-password",
			strings.NewReader(`{"newPassword":"weak"}`))
		req.SetPathValue("userId", "u")
		platformResetPasswordHandler(&stubPlatform{
			resetErr: errors.New("studiosvc: password must be at least 8 characters"),
		})(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("weak-password must 400, got %d", rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "password") {
			t.Fatalf("error must mention password: %s", rr.Body.String())
		}
	})
}
