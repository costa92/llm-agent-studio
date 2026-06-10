package httpapi

import (
	"context"

	authzrole "github.com/costa92/llm-agent-authz/role"
)

type stubResolver struct{}

func (stubResolver) ResolveRole(_ context.Context, _, _, _, _ string) (authzrole.Role, error) {
	return authzrole.RoleNone, nil
}
