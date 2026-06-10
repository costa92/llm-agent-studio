package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	authztoken "github.com/costa92/llm-agent-authz/token"
)

func TestUnauthenticatedRejected(t *testing.T) {
	iss := authztoken.NewIssuer([]byte("s"), time.Minute)
	mux := NewMux(Deps{Issuer: iss, RoleResolver: stubResolver{}})
	req := httptest.NewRequest("GET", "/api/projects/x", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("code=%d want 401", rr.Code)
	}
}

func TestInvalidTokenRejected(t *testing.T) {
	iss := authztoken.NewIssuer([]byte("s"), time.Minute)
	mux := NewMux(Deps{Issuer: iss, RoleResolver: stubResolver{}})
	req := httptest.NewRequest("POST", "/api/orgs", nil)
	req.Header.Set("Authorization", "Bearer not-a-jwt")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("code=%d want 401", rr.Code)
	}
}
