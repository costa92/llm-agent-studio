package studiosvc

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// OrgList lists the orgs a user belongs to. The authz store owns the auth_*
// tables but exposes no "orgs for user" query, so we read auth_membership ⋈
// auth_org directly over the shared pool (org-level memberships only —
// scope_kind='org' — which is what grants org access; mirrors how Org grants
// the creator an org-level org_admin row).
type OrgList struct {
	pool *pgxpool.Pool
}

// NewOrgList builds an OrgList reader.
func NewOrgList(pool *pgxpool.Pool) *OrgList { return &OrgList{pool: pool} }

// OrgsForUser returns the user's orgs as JSON-serializable maps {id,name,role},
// ordered by name. Empty (non-nil) slice when the user has no memberships.
func (l *OrgList) OrgsForUser(ctx context.Context, userID string) ([]map[string]any, error) {
	if userID == "" {
		return nil, fmt.Errorf("studiosvc: userID required")
	}
	rows, err := l.pool.Query(ctx,
		`SELECT o.id, o.name, m.role
		   FROM auth_membership m
		   JOIN auth_org o ON o.id = m.org_id
		  WHERE m.user_id = $1 AND m.scope_kind = 'org'
		  ORDER BY o.name ASC`, userID)
	if err != nil {
		return nil, fmt.Errorf("studiosvc: orgs for user: %w", err)
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		var id, name, role string
		if err := rows.Scan(&id, &name, &role); err != nil {
			return nil, fmt.Errorf("studiosvc: scan org: %w", err)
		}
		out = append(out, map[string]any{"id": id, "name": name, "role": role})
	}
	return out, rows.Err()
}
