package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	authzrole "github.com/costa92/llm-agent-authz/role"

	"github.com/costa92/llm-agent-studio/internal/studiosvc"
)

// stubMembers is a fake MemberService recording last inputs, with injectable results.
type stubMembers struct {
	members []studiosvc.OrgMember
	added   studiosvc.OrgMember
	addErr  error
	setErr  error
	rmErr   error

	lastOrg    string
	lastEmail  string
	lastRole   authzrole.Role
	lastUserID string
}

func (s *stubMembers) ListMembers(_ context.Context, orgID string) ([]studiosvc.OrgMember, error) {
	s.lastOrg = orgID
	return s.members, nil
}
func (s *stubMembers) AddMemberByEmail(_ context.Context, orgID, email string, r authzrole.Role) (studiosvc.OrgMember, error) {
	s.lastOrg, s.lastEmail, s.lastRole = orgID, email, r
	return s.added, s.addErr
}
func (s *stubMembers) SetMemberRole(_ context.Context, orgID, userID string, r authzrole.Role) error {
	s.lastOrg, s.lastUserID, s.lastRole = orgID, userID, r
	return s.setErr
}
func (s *stubMembers) RemoveMember(_ context.Context, orgID, userID string) error {
	s.lastOrg, s.lastUserID = orgID, userID
	return s.rmErr
}

// TestListMembersHandler proves GET wraps members in {items} (nil → []).
func TestListMembersHandler(t *testing.T) {
	st := &stubMembers{members: []studiosvc.OrgMember{{UserID: "u1", Email: "a@x.com", Role: "org_admin"}}}
	rr := httptest.NewRecorder()
	req := storageReq("GET", "/api/orgs/o1/members", "")
	req.SetPathValue("org", "o1")
	listMembersHandler(st)(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"items"`) ||
		!strings.Contains(rr.Body.String(), `"role":"org_admin"`) {
		t.Fatalf("list members body: %d %s", rr.Code, rr.Body.String())
	}
	if st.lastOrg != "o1" {
		t.Fatalf("org not passed: %q", st.lastOrg)
	}

	rr2 := httptest.NewRecorder()
	listMembersHandler(&stubMembers{})(rr2, storageReq("GET", "/api/orgs/o1/members", ""))
	if !strings.Contains(rr2.Body.String(), `"items":[]`) {
		t.Fatalf("nil members want items:[], got %s", rr2.Body.String())
	}
}

// TestMeRoleHandler proves GET /members/me echoes the caller's resolved role
// string for viewer/editor/admin (userId="" absent test auth ctx, role is the
// load-bearing field the frontend reads). 非成员 403 由 scoped(roleViewer) 路由层保证。
func TestMeRoleHandler(t *testing.T) {
	for _, role := range []authzrole.Role{authzrole.RoleViewer, authzrole.RoleEditor, authzrole.RoleAdmin} {
		rr := httptest.NewRecorder()
		req := storageReq("GET", "/api/orgs/o1/members/me", "")
		req.SetPathValue("org", "o1")
		meRoleHandler(stubRoleResolver{role: role})(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("me role want 200, got %d", rr.Code)
		}
		if !strings.Contains(rr.Body.String(), `"role":"`+string(role)+`"`) {
			t.Fatalf("me role want role=%q in body, got %s", string(role), rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), `"userId"`) {
			t.Fatalf("me role missing userId key: %s", rr.Body.String())
		}
	}
}

// TestAddMemberHandler proves POST happy → 201 OrgMember, missing email → 400,
// bad role → 400, ErrUserNotFound → 404.
func TestAddMemberHandler(t *testing.T) {
	st := &stubMembers{added: studiosvc.OrgMember{UserID: "u-9", Email: "a@x.com", Role: "editor"}}
	rr := httptest.NewRecorder()
	req := storageReq("POST", "/api/orgs/o1/members", `{"email":"a@x.com","role":"editor"}`)
	req.SetPathValue("org", "o1")
	addMemberHandler(st)(rr, req)
	if rr.Code != http.StatusCreated || !strings.Contains(rr.Body.String(), `"userId":"u-9"`) ||
		!strings.Contains(rr.Body.String(), `"role":"editor"`) {
		t.Fatalf("add want 201 OrgMember: %d %s", rr.Code, rr.Body.String())
	}
	if st.lastEmail != "a@x.com" || st.lastRole != authzrole.RoleEditor {
		t.Fatalf("add args not passed: email=%q role=%q", st.lastEmail, st.lastRole)
	}

	// Missing email → 400.
	rr2 := httptest.NewRecorder()
	addMemberHandler(st)(rr2, storageReq("POST", "/api/orgs/o1/members", `{"role":"editor"}`))
	if rr2.Code != http.StatusBadRequest {
		t.Fatalf("missing email want 400, got %d", rr2.Code)
	}

	// Bad role → 400.
	rr3 := httptest.NewRecorder()
	addMemberHandler(st)(rr3, storageReq("POST", "/api/orgs/o1/members", `{"email":"a@x.com","role":"superuser"}`))
	if rr3.Code != http.StatusBadRequest {
		t.Fatalf("bad role want 400, got %d", rr3.Code)
	}

	// ErrUserNotFound → 404.
	rr4 := httptest.NewRecorder()
	addMemberHandler(&stubMembers{addErr: studiosvc.ErrUserNotFound})(rr4,
		storageReq("POST", "/api/orgs/o1/members", `{"email":"nobody@x.com","role":"viewer"}`))
	if rr4.Code != http.StatusNotFound {
		t.Fatalf("unknown email want 404, got %d", rr4.Code)
	}
}

// TestSetMemberRoleHandler proves PUT happy → 200 {ok:true}, empty userId → 400,
// bad role → 400, ErrMemberNotFound → 404, ErrLastOrgAdmin → 409.
func TestSetMemberRoleHandler(t *testing.T) {
	st := &stubMembers{}
	rr := httptest.NewRecorder()
	req := storageReq("PUT", "/api/orgs/o1/members/u1", `{"role":"admin"}`)
	req.SetPathValue("org", "o1")
	req.SetPathValue("userId", "u1")
	setMemberRoleHandler(st)(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"ok":true`) {
		t.Fatalf("set want 200 {ok:true}: %d %s", rr.Code, rr.Body.String())
	}
	if st.lastUserID != "u1" || st.lastRole != authzrole.RoleAdmin {
		t.Fatalf("set args not passed: userId=%q role=%q", st.lastUserID, st.lastRole)
	}

	// Empty userId → 400.
	rr2 := httptest.NewRecorder()
	setMemberRoleHandler(st)(rr2, storageReq("PUT", "/api/orgs/o1/members/", `{"role":"admin"}`))
	if rr2.Code != http.StatusBadRequest {
		t.Fatalf("empty userId want 400, got %d", rr2.Code)
	}

	// Bad role → 400.
	rr3 := httptest.NewRecorder()
	req3 := storageReq("PUT", "/api/orgs/o1/members/u1", `{"role":"nope"}`)
	req3.SetPathValue("userId", "u1")
	setMemberRoleHandler(st)(rr3, req3)
	if rr3.Code != http.StatusBadRequest {
		t.Fatalf("bad role want 400, got %d", rr3.Code)
	}

	// ErrMemberNotFound → 404.
	rr4 := httptest.NewRecorder()
	req4 := storageReq("PUT", "/api/orgs/o1/members/u1", `{"role":"viewer"}`)
	req4.SetPathValue("userId", "u1")
	setMemberRoleHandler(&stubMembers{setErr: studiosvc.ErrMemberNotFound})(rr4, req4)
	if rr4.Code != http.StatusNotFound {
		t.Fatalf("non-member want 404, got %d", rr4.Code)
	}

	// ErrLastOrgAdmin → 409.
	rr5 := httptest.NewRecorder()
	req5 := storageReq("PUT", "/api/orgs/o1/members/u1", `{"role":"viewer"}`)
	req5.SetPathValue("userId", "u1")
	setMemberRoleHandler(&stubMembers{setErr: studiosvc.ErrLastOrgAdmin})(rr5, req5)
	if rr5.Code != http.StatusConflict {
		t.Fatalf("last org admin want 409, got %d", rr5.Code)
	}
}

// TestRemoveMemberHandler proves DELETE happy → 200 {ok:true}, empty userId → 400,
// ErrMemberNotFound → 404, ErrLastOrgAdmin → 409.
func TestRemoveMemberHandler(t *testing.T) {
	st := &stubMembers{}
	rr := httptest.NewRecorder()
	req := storageReq("DELETE", "/api/orgs/o1/members/u1", "")
	req.SetPathValue("org", "o1")
	req.SetPathValue("userId", "u1")
	removeMemberHandler(st)(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"ok":true`) {
		t.Fatalf("remove want 200 {ok:true}: %d %s", rr.Code, rr.Body.String())
	}
	if st.lastUserID != "u1" {
		t.Fatalf("remove userId not passed: %q", st.lastUserID)
	}

	// Empty userId → 400.
	rr2 := httptest.NewRecorder()
	removeMemberHandler(st)(rr2, storageReq("DELETE", "/api/orgs/o1/members/", ""))
	if rr2.Code != http.StatusBadRequest {
		t.Fatalf("empty userId want 400, got %d", rr2.Code)
	}

	// ErrMemberNotFound → 404.
	rr3 := httptest.NewRecorder()
	req3 := storageReq("DELETE", "/api/orgs/o1/members/u1", "")
	req3.SetPathValue("userId", "u1")
	removeMemberHandler(&stubMembers{rmErr: studiosvc.ErrMemberNotFound})(rr3, req3)
	if rr3.Code != http.StatusNotFound {
		t.Fatalf("non-member want 404, got %d", rr3.Code)
	}

	// ErrLastOrgAdmin → 409.
	rr4 := httptest.NewRecorder()
	req4 := storageReq("DELETE", "/api/orgs/o1/members/u1", "")
	req4.SetPathValue("userId", "u1")
	removeMemberHandler(&stubMembers{rmErr: studiosvc.ErrLastOrgAdmin})(rr4, req4)
	if rr4.Code != http.StatusConflict {
		t.Fatalf("last org admin want 409, got %d", rr4.Code)
	}
}
