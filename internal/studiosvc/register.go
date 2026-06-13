package studiosvc

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/costa92/llm-agent-authz/password"
	authzstore "github.com/costa92/llm-agent-authz/store"
	"github.com/costa92/llm-agent-studio/internal/mail"
)

// ErrEmailExists is returned by Register.Create when the email is already taken
// (maps the authz store's duplicate-email unique violation to a 409 at the
// handler). Handlers branch on errors.Is(err, ErrEmailExists).
var ErrEmailExists = errors.New("studiosvc: email already exists")

// platformGranter 是 Register 的 top-up 钩子最小接口（由 *Platform 满足）。仅取
// Grant，避免 Register 反向依赖整个 Platform 服务。
type platformGranter interface {
	Grant(ctx context.Context, userID string) error
}

// Register creates self-serve user accounts over the authz store (mirrors Org).
type Register struct {
	authz *authzstore.Store
	mail  *mail.Client

	// platform + adminEmails 是平台管理员“注册即授予”的 top-up 钩子：当新用户的邮箱
	// 命中 env 种子名单时，建账后立即授予平台管理员（env 种子在 SeedFromEmails 里跳过
	// 了尚未注册的邮箱，故在此补上）。二者皆为可选——未接线时 Create 行为不变。
	platform    platformGranter
	adminEmails map[string]struct{}
}

// NewRegister builds a Register adapter.
func NewRegister(az *authzstore.Store) *Register { return &Register{authz: az} }

// WithMail chains the mail client into Register.
func (r *Register) WithMail(m *mail.Client) *Register {
	r.mail = m
	return r
}

// WithPlatformTopUp 接入平台管理员的“注册即授予”钩子：emails（已小写 trim）命中时，
// 新用户建账后调用 platform.Grant。返回自身以便链式接线。
func (r *Register) WithPlatformTopUp(p platformGranter, emails []string) *Register {
	set := make(map[string]struct{}, len(emails))
	for _, e := range emails {
		if e != "" {
			set[e] = struct{}{}
		}
	}
	r.platform, r.adminEmails = p, set
	return r
}

func generateCode() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	val := (int(b[0])<<16 | int(b[1])<<8 | int(b[2])) % 900000
	return fmt.Sprintf("%06d", 100000+val)
}

// Create hashes the password (authz crypto) and creates the user. On a duplicate
// email (authz store ErrConflict, PG unique violation) it returns ErrEmailExists.
// 建账成功后，若邮箱命中平台管理员种子名单，则补授平台管理员（top-up）。
// 生成 6 位邮箱验证码，设置 is_verified 为 false 并写入 DB，然后发送验证邮件。
func (r *Register) Create(ctx context.Context, email, plain string) (string, error) {
	hash, err := password.Hash(plain)
	if err != nil {
		return "", fmt.Errorf("studiosvc: hash password: %w", err)
	}
	uid, err := r.authz.CreateUser(ctx, email, hash)
	if errors.Is(err, authzstore.ErrConflict) {
		return "", ErrEmailExists
	}
	if err != nil {
		return "", fmt.Errorf("studiosvc: create user: %w", err)
	}

	// Generate and set verification code (valid for 15 minutes)
	code := generateCode()
	expiresAt := time.Now().Add(15 * time.Minute)
	if err := r.authz.SetUserVerificationCode(ctx, email, code, expiresAt); err != nil {
		return "", fmt.Errorf("studiosvc: set verification code: %w", err)
	}

	// Send verification email
	if r.mail != nil {
		if err := r.mail.SendVerificationCode(ctx, email, code); err != nil {
			return "", fmt.Errorf("studiosvc: send verification mail: %w", err)
		}
	}

	if r.platform != nil {
		if _, ok := r.adminEmails[strings.ToLower(strings.TrimSpace(email))]; ok {
			if err := r.platform.Grant(ctx, uid); err != nil {
				return "", fmt.Errorf("studiosvc: top-up platform admin: %w", err)
			}
		}
	}
	return uid, nil
}

// Verify checks the verification code. If valid, it returns ok=true and the userID.
func (r *Register) Verify(ctx context.Context, email, code string) (bool, string, error) {
	u, err := r.authz.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, authzstore.ErrNotFound) {
			return false, "", nil
		}
		return false, "", fmt.Errorf("studiosvc: load user: %w", err)
	}
	ok, err := r.authz.VerifyUserCode(ctx, email, code, time.Now())
	if err != nil {
		return false, "", fmt.Errorf("studiosvc: verify code: %w", err)
	}
	if !ok {
		return false, "", nil
	}
	return true, u.ID, nil
}

// Resend generates a new verification code and resends the email.
func (r *Register) Resend(ctx context.Context, email string) error {
	_, err := r.authz.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, authzstore.ErrNotFound) {
			return errors.New("studiosvc: user not found")
		}
		return fmt.Errorf("studiosvc: load user: %w", err)
	}

	code := generateCode()
	expiresAt := time.Now().Add(15 * time.Minute)
	if err := r.authz.SetUserVerificationCode(ctx, email, code, expiresAt); err != nil {
		return fmt.Errorf("studiosvc: set verification code: %w", err)
	}

	if r.mail != nil {
		if err := r.mail.SendVerificationCode(ctx, email, code); err != nil {
			return fmt.Errorf("studiosvc: send verification mail: %w", err)
		}
	}
	return nil
}
