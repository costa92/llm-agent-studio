// Package review is the HITL service (spec §7.4): admin accept/reject/regenerate
// on pending_acceptance assets. accept→accepted, reject→rejected; regenerate
// rejects the current asset, creates a v+1 child (parent lineage) in 'generating'
// and spawns a 'ready' asset todo carrying the edited prompt + parentAssetId so
// the worker's runAsset regenerate branch (T10) produces the new bytes. A
// transition on a non-pending asset returns ErrConflict (HTTP 409). admin-only
// is enforced at the httpapi mount (RequireScopeRole admin), not here.
package review

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/costa92/llm-agent-studio/internal/assets"
	"github.com/costa92/llm-agent-studio/internal/todos"
)

// ErrConflict is returned when an asset is not in pending_acceptance (409).
var ErrConflict = errors.New("review: asset not pending_acceptance")

// Service performs the HITL transitions.
type Service struct {
	assets *assets.Store
	todos  *todos.Store
}

// New builds a Service.
func New(as *assets.Store, td *todos.Store) *Service { return &Service{assets: as, todos: td} }

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Accept moves a pending asset to accepted.
func (s *Service) Accept(ctx context.Context, assetID string) error {
	return s.transition(ctx, assetID, "accepted")
}

// Reject moves a pending asset to rejected.
func (s *Service) Reject(ctx context.Context, assetID string) error {
	return s.transition(ctx, assetID, "rejected")
}

func (s *Service) transition(ctx context.Context, assetID, to string) error {
	ok, err := s.assets.TransitionStatus(ctx, assetID, "pending_acceptance", to)
	if err != nil {
		return err
	}
	if !ok {
		return ErrConflict
	}
	return nil
}

// Regenerate rejects the pending asset, creates a v+1 child (lineage) and a
// ready asset todo with the edited prompt. Returns (newAssetID, todoID).
func (s *Service) Regenerate(ctx context.Context, assetID, editedPrompt string) (string, string, error) {
	parent, err := s.assets.Get(ctx, assetID)
	if err != nil {
		return "", "", err
	}
	// Guard: only a pending asset can be regenerated (409 otherwise).
	ok, err := s.assets.TransitionStatus(ctx, assetID, "pending_acceptance", "rejected")
	if err != nil {
		return "", "", err
	}
	if !ok {
		return "", "", ErrConflict
	}
	prompt := editedPrompt
	if prompt == "" {
		prompt = parent.Prompt
	}
	child, err := s.assets.CreateVersion(ctx, assetID, assets.CreateInput{
		ProjectID: parent.ProjectID, ShotID: parent.ShotID, Type: parent.Type,
		Prompt: prompt, Style: parent.Style, Status: "generating",
	})
	if err != nil {
		return "", "", err
	}
	// Spawn a ready asset todo (worker runAsset regenerate branch picks it up).
	input, _ := json.Marshal(map[string]string{
		"shotId": parent.ShotID, "shotPrompt": parent.Prompt, "style": parent.Style,
		"parentAssetId": assetID, "editedPrompt": prompt,
	})
	todoID := newID()
	if _, err := s.todos.AddSingleReady(ctx, child.ProjectID, todoID, "asset", []string{assetID}, input); err != nil {
		return "", "", fmt.Errorf("review: spawn regenerate todo: %w", err)
	}
	return child.ID, todoID, nil
}
