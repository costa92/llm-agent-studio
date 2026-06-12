package studiosvc

import (
	"context"
	"errors"
	"fmt"

	"github.com/costa92/llm-agent-authz/password"
	authzstore "github.com/costa92/llm-agent-authz/store"
)

// ErrEmailExists is returned by Register.Create when the email is already taken
// (maps the authz store's duplicate-email unique violation to a 409 at the
// handler). Handlers branch on errors.Is(err, ErrEmailExists).
var ErrEmailExists = errors.New("studiosvc: email already exists")

// Register creates self-serve user accounts over the authz store (mirrors Org).
type Register struct {
	authz *authzstore.Store
}

// NewRegister builds a Register adapter.
func NewRegister(az *authzstore.Store) *Register { return &Register{authz: az} }

// Create hashes the password (authz crypto) and creates the user. On a duplicate
// email (authz store ErrConflict, PG unique violation) it returns ErrEmailExists.
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
	return uid, nil
}
