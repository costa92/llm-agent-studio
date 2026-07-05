package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	authzrole "github.com/costa92/llm-agent-authz/role"
	authztoken "github.com/costa92/llm-agent-authz/token"

	"github.com/costa92/llm-agent-studio/internal/project"
	"github.com/costa92/llm-agent-studio/internal/projectstate"
)

// delProjStub 是带软删状态的内存 ProjectStore：语义镜像 *project.Store
// （Get 对已删 → ErrNotFound；OrgIDForProject 刻意不过滤；SoftDelete 幂等返
// ErrNotFound），供 mux 级 auth→RBAC→liveness 门禁链测试。
type delProjStub struct {
	orgID   string
	deleted bool
}

func (s *delProjStub) Create(context.Context, project.CreateInput) (project.Project, error) {
	return project.Project{}, nil
}
func (s *delProjStub) Get(_ context.Context, id string) (project.Project, error) {
	if s.deleted {
		return project.Project{}, project.ErrNotFound
	}
	return project.Project{ID: id, OrgID: s.orgID, Name: "p", Status: "draft"}, nil
}
func (s *delProjStub) ListByOrg(context.Context, string, int, string) ([]project.Project, string, error) {
	return nil, "", nil
}
func (s *delProjStub) Update(context.Context, string, project.UpdateInput) (project.Project, error) {
	return project.Project{}, nil
}
func (s *delProjStub) SetStatus(context.Context, string, string) error { return nil }
func (s *delProjStub) SetCover(context.Context, string, string) error  { return nil }
func (s *delProjStub) Cancel(context.Context, string) error            { return nil }
func (s *delProjStub) Deleted(context.Context, string) (bool, error)   { return s.deleted, nil }
func (s *delProjStub) SoftDelete(context.Context, string) error {
	if s.deleted {
		return project.ErrNotFound
	}
	s.deleted = true
	return nil
}
func (s *delProjStub) OrgIDForProject(context.Context, string) (string, error) {
	return s.orgID, nil // 已删仍解析：RBAC 先行，liveness 门禁再 404
}
func (s *delProjStub) ListPlans(context.Context, string) ([]project.Plan, error) { return nil, nil }
func (s *delProjStub) LoadState(context.Context, string, string) (projectstate.ProjectState, error) {
	return projectstate.ProjectState{}, nil
}

// mapResolver 按 "userID/orgID" 查角色（缺省 RoleNone）——模拟跨租户：用户在
// 自己 org 有角色，在项目所属 org 无。
type mapResolver map[string]authzrole.Role

func (m mapResolver) ResolveRole(_ context.Context, userID, orgID, _, _ string) (authzrole.Role, error) {
	return m[userID+"/"+orgID], nil
}

func delTestMux(t *testing.T, ps ProjectStore) (*http.ServeMux, func(user string) string) {
	t.Helper()
	iss := authztoken.NewIssuer([]byte("s"), time.Minute)
	mux := NewMux(Deps{
		Issuer: iss,
		RoleResolver: mapResolver{
			"admin/o1":  authzrole.RoleAdmin,
			"editor/o1": authzrole.RoleEditor,
			// intruder 在别的 org 是 admin，但在 o1 无角色（跨租户）。
			"intruder/o2": authzrole.RoleAdmin,
		},
		Projects: ps,
	})
	return mux, func(user string) string {
		tok, err := iss.Issue(user, time.Now())
		if err != nil {
			t.Fatalf("issue token: %v", err)
		}
		return tok
	}
}

func doDelete(mux *http.ServeMux, tok, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("DELETE", path, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

// TestDeleteProjectRBACAndIdempotency：spec §3 验收——跨租户删除 403、editor 403
// （roleAdmin 门禁）、admin 删除成功、重复删除 404（幂等约定）、删除后 GET 404。
func TestDeleteProjectRBACAndIdempotency(t *testing.T) {
	ps := &delProjStub{orgID: "o1"}
	mux, token := delTestMux(t, ps)

	// 跨租户（o2 的 admin）→ RBAC 403，且不落删除。
	if rr := doDelete(mux, token("intruder"), "/api/projects/p1"); rr.Code != http.StatusForbidden {
		t.Fatalf("cross-tenant delete: code=%d want 403", rr.Code)
	}
	if ps.deleted {
		t.Fatalf("cross-tenant delete must not tombstone the project")
	}
	// 本 org editor → roleAdmin 门禁 403。
	if rr := doDelete(mux, token("editor"), "/api/projects/p1"); rr.Code != http.StatusForbidden {
		t.Fatalf("editor delete: code=%d want 403", rr.Code)
	}
	// admin → 200 且落删除。
	if rr := doDelete(mux, token("admin"), "/api/projects/p1"); rr.Code != http.StatusOK {
		t.Fatalf("admin delete: code=%d want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	if !ps.deleted {
		t.Fatalf("admin delete did not tombstone the project")
	}
	// 重复删除 → liveness 门禁 404（幂等约定：与其余 DELETE 端点对 missing 一致）。
	if rr := doDelete(mux, token("admin"), "/api/projects/p1"); rr.Code != http.StatusNotFound {
		t.Fatalf("second delete: code=%d want 404", rr.Code)
	}
}

// TestDeletedProjectReadPaths404：删除后一切 project-scoped 路由（含 GET 项目详情
// 与 cancel 这类不经 Get 的端点）对 org 成员一律 404。
func TestDeletedProjectReadPaths404(t *testing.T) {
	ps := &delProjStub{orgID: "o1", deleted: true}
	mux, token := delTestMux(t, ps)

	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/projects/p1"},
		{"GET", "/api/projects/p1/plans"},
		{"POST", "/api/projects/p1/cancel"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Header.Set("Authorization", "Bearer "+token("admin"))
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("%s %s on deleted project: code=%d want 404", tc.method, tc.path, rr.Code)
		}
	}
	// 跨租户探测者对已删项目仍拿 403（RBAC 先行，不泄露删除状态）。
	req := httptest.NewRequest("GET", "/api/projects/p1", nil)
	req.Header.Set("Authorization", "Bearer "+token("intruder"))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("cross-tenant probe of deleted project: code=%d want 403", rr.Code)
	}
}
