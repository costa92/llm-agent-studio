package studiosvc

import (
	"context"
	"errors"
	"os"
	"testing"

	authzstore "github.com/costa92/llm-agent-authz/store"

	"github.com/costa92/llm-agent-studio/internal/storage"
)

func TestRegisterCreateDuplicateEmail(t *testing.T) {
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
	r := NewRegister(az)
	email := "reg_" + randHexSvc() + "@x.com"

	uid, err := r.Create(ctx, email, "password123")
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	if uid == "" {
		t.Fatalf("first create returned empty user id")
	}

	// Same email again → ErrEmailExists.
	if _, err := r.Create(ctx, email, "password123"); !errors.Is(err, ErrEmailExists) {
		t.Fatalf("duplicate create err = %v, want ErrEmailExists", err)
	}
}

// TestRegisterPlatformTopUp proves a new user whose email is in the seed list is
// granted platform admin at register; an off-list email is not. Mirrors the
// startup-seed + register-top-up bootstrap (env-seeded user registers later).
func TestRegisterPlatformTopUp(t *testing.T) {
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
	p := NewPlatform(az, st.GORM())
	if err := p.EnsureSentinelOrg(ctx); err != nil {
		t.Fatalf("ensure sentinel org: %v", err)
	}

	seedEmail := "topup_" + randHexSvc() + "@x.com"
	offEmail := "offlist_" + randHexSvc() + "@x.com"
	r := NewRegister(az).WithPlatformTopUp(p, []string{seedEmail})

	uidSeed, err := r.Create(ctx, seedEmail, "password123")
	if err != nil {
		t.Fatalf("create seed user: %v", err)
	}
	if ok, _ := p.IsPlatformAdmin(ctx, uidSeed); !ok {
		t.Fatalf("seed-list user must be platform admin after register")
	}

	uidOff, err := r.Create(ctx, offEmail, "password123")
	if err != nil {
		t.Fatalf("create off-list user: %v", err)
	}
	if ok, _ := p.IsPlatformAdmin(ctx, uidOff); ok {
		t.Fatalf("off-list user must NOT be platform admin")
	}
}

func TestRegisterVerificationFlow(t *testing.T) {
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

	r := NewRegister(az)
	email := "verify_" + randHexSvc() + "@x.com"

	uid, err := r.Create(ctx, email, "password123")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// User should not be verified initially
	u, err := az.GetUserByEmail(ctx, email)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if u.IsVerified {
		t.Fatalf("expected user to be unverified initially")
	}
	if u.VerificationCode == "" {
		t.Fatalf("expected verification code to be set")
	}

	// Try verifying with wrong code
	ok, verifiedUID, err := r.Verify(ctx, email, "000000")
	if err != nil {
		t.Fatalf("verify wrong code: %v", err)
	}
	if ok || verifiedUID != "" {
		t.Fatalf("expected wrong code to fail verification")
	}

	// Verify with correct code
	ok, verifiedUID, err = r.Verify(ctx, email, u.VerificationCode)
	if err != nil {
		t.Fatalf("verify correct code: %v", err)
	}
	if !ok || verifiedUID != uid {
		t.Fatalf("expected correct code to verify successfully, got ok=%v, uid=%q", ok, verifiedUID)
	}

	// User should be verified now
	u2, err := az.GetUserByEmail(ctx, email)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if !u2.IsVerified {
		t.Fatalf("expected user to be verified after correct code entry")
	}
	if u2.VerificationCode != "" {
		t.Fatalf("expected verification code to be cleared after success")
	}

	// Resend code test
	email2 := "resend_" + randHexSvc() + "@x.com"
	_, err = r.Create(ctx, email2, "password123")
	if err != nil {
		t.Fatalf("create user 2: %v", err)
	}

	u3, err := az.GetUserByEmail(ctx, email2)
	if err != nil {
		t.Fatalf("get user 2: %v", err)
	}

	err = r.Resend(ctx, email2)
	if err != nil {
		t.Fatalf("resend: %v", err)
	}

	u4, err := az.GetUserByEmail(ctx, email2)
	if err != nil {
		t.Fatalf("get user 2 after resend: %v", err)
	}
	if u4.VerificationCode == u3.VerificationCode {
		t.Fatalf("expected verification code to change after resend")
	}
}
