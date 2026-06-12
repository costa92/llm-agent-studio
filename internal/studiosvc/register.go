package studiosvc

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/costa92/llm-agent-authz/password"
	authzstore "github.com/costa92/llm-agent-authz/store"
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

	// platform + adminEmails 是平台管理员“注册即授予”的 top-up 钩子：当新用户的邮箱
	// 命中 env 种子名单时，建账后立即授予平台管理员（env 种子在 SeedFromEmails 里跳过
	// 了尚未注册的邮箱，故在此补上）。二者皆为可选——未接线时 Create 行为不变。
	platform    platformGranter
	adminEmails map[string]struct{}
}

// NewRegister builds a Register adapter.
func NewRegister(az *authzstore.Store) *Register { return &Register{authz: az} }

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

// Create hashes the password (authz crypto) and creates the user. On a duplicate
// email (authz store ErrConflict, PG unique violation) it returns ErrEmailExists.
// 建账成功后，若邮箱命中平台管理员种子名单，则补授平台管理员（top-up）。
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
	if r.platform != nil {
		if _, ok := r.adminEmails[strings.ToLower(strings.TrimSpace(email))]; ok {
			if err := r.platform.Grant(ctx, uid); err != nil {
				return "", fmt.Errorf("studiosvc: top-up platform admin: %w", err)
			}
		}
	}
	return uid, nil
}
