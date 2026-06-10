// Package studiosvc adapts the studio domain stores to the httpapi ports:
// org bootstrap (authz) and artifact reads (todos/scripts/shots).
package studiosvc

import (
	"context"
	"fmt"

	authzrole "github.com/costa92/llm-agent-authz/role"
	authzstore "github.com/costa92/llm-agent-authz/store"
)

// Org bootstraps orgs + creator org_admin membership over the authz store.
type Org struct {
	authz *authzstore.Store
}

// NewOrg builds an Org adapter.
func NewOrg(az *authzstore.Store) *Org { return &Org{authz: az} }

// CreateOrg creates an org and grants the creator an ORG-LEVEL org_admin
// membership (scope_kind="org", scope_id=nil). Org-level rows match every
// project-scope ResolveRole (scope_id IS NULL OR scope_id=$4), so the creator
// can create/list projects in the org. Mirrors orgkb.CreateOrg with "org".
func (o *Org) CreateOrg(ctx context.Context, name, creatorUserID string) (string, error) {
	if name == "" || creatorUserID == "" {
		return "", fmt.Errorf("studiosvc: org name and creatorUserID required")
	}
	orgID, err := o.authz.CreateOrg(ctx, name)
	if err != nil {
		return "", fmt.Errorf("studiosvc: create org: %w", err)
	}
	if err := o.authz.UpsertMembership(ctx, orgID, creatorUserID, "org", nil, authzrole.RoleOrgAdmin); err != nil {
		return "", fmt.Errorf("studiosvc: grant creator org_admin: %w", err)
	}
	return orgID, nil
}
