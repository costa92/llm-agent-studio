package studiosvc

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/lib/pq"
	"gorm.io/gorm"
)

// Artifacts reads todos/scripts/shots for the artifact endpoints.
type Artifacts struct {
	db *gorm.DB
}

// NewArtifacts builds an Artifacts reader.
func NewArtifacts(db *gorm.DB) *Artifacts { return &Artifacts{db: db} }

// Todos lists a project's todos as JSON-serializable maps.
func (a *Artifacts) Todos(ctx context.Context, projectID string, planID string) ([]map[string]any, error) {
	var rows *sql.Rows
	var err error
	if planID == "" {
		rows, err = a.db.WithContext(ctx).Raw(
			`SELECT id, type, status, depends_on, attempts, error FROM todos WHERE project_id=$1 ORDER BY created_at ASC`,
			projectID).Rows()
	} else {
		rows, err = a.db.WithContext(ctx).Raw(
			`SELECT id, type, status, depends_on, attempts, error FROM todos WHERE project_id=$1 AND plan_id=$2 ORDER BY created_at ASC`,
			projectID, planID).Rows()
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		var id, typ, status, errMsg string
		var deps pq.StringArray
		var attempts int
		if err := rows.Scan(&id, &typ, &status, &deps, &attempts, &errMsg); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"id": id, "type": typ, "status": status, "dependsOn": []string(deps),
			"attempts": attempts, "error": errMsg,
		})
	}
	return out, rows.Err()
}

// Script returns the latest script's content_json for a project (ok=false if
// none yet). When todoID is non-empty, returns the artifact for that specific
// workflow node only (scoped by scripts.todo_id), ignoring planID. When todoID
// is empty, falls back to the existing per-planID / latest behavior.
func (a *Artifacts) Script(ctx context.Context, projectID string, planID string, todoID string) (json.RawMessage, bool, error) {
	var content []byte
	var err error
	if todoID != "" {
		err = a.db.WithContext(ctx).Raw(
			`SELECT content_json FROM scripts WHERE project_id=$1 AND todo_id=$2 ORDER BY created_at DESC LIMIT 1`,
			projectID, todoID).Row().Scan(&content)
	} else if planID == "" {
		err = a.db.WithContext(ctx).Raw(
			`SELECT content_json FROM scripts WHERE project_id=$1 ORDER BY created_at DESC LIMIT 1`,
			projectID).Row().Scan(&content)
	} else {
		err = a.db.WithContext(ctx).Raw(
			`SELECT s.content_json FROM scripts s
			 JOIN todos t ON s.todo_id = t.id
			 WHERE s.project_id=$1 AND t.plan_id=$2
			 ORDER BY s.created_at DESC LIMIT 1`,
			projectID, planID).Row().Scan(&content)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return content, true, nil
}

// Shots lists a project's shots ordered by ordering. When todoID is non-empty,
// returns shots for that specific workflow node only (scoped by shots.todo_id),
// ignoring planID. When todoID is empty, falls back to the existing per-planID
// / all-shots behavior.
func (a *Artifacts) Shots(ctx context.Context, projectID string, planID string, todoID string) ([]map[string]any, error) {
	var rows *sql.Rows
	var err error
	if todoID != "" {
		rows, err = a.db.WithContext(ctx).Raw(
			`SELECT id, shot_no, camera, scene, action, prompt, duration FROM shots WHERE project_id=$1 AND todo_id=$2 ORDER BY ordering ASC`,
			projectID, todoID).Rows()
	} else if planID == "" {
		rows, err = a.db.WithContext(ctx).Raw(
			`SELECT id, shot_no, camera, scene, action, prompt, duration FROM shots WHERE project_id=$1 ORDER BY ordering ASC`,
			projectID).Rows()
	} else {
		rows, err = a.db.WithContext(ctx).Raw(
			`SELECT s.id, s.shot_no, s.camera, s.scene, s.action, s.prompt, s.duration FROM shots s
			 JOIN todos t ON s.todo_id = t.id
			 WHERE s.project_id=$1 AND t.plan_id=$2
			 ORDER BY s.ordering ASC`,
			projectID, planID).Rows()
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		var id, camera, scene, action, prompt string
		var shotNo, duration int
		if err := rows.Scan(&id, &shotNo, &camera, &scene, &action, &prompt, &duration); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"id": id, "shotNo": shotNo, "camera": camera, "scene": scene,
			"action": action, "prompt": prompt, "duration": duration,
		})
	}
	return out, rows.Err()
}

// Assets lists a project's assets, newest first, optionally filtered by status.
func (a *Artifacts) Assets(ctx context.Context, projectID, planID, status string) ([]map[string]any, error) {
	var rows *sql.Rows
	var err error
	if planID == "" {
		q := `SELECT id, shot_id, type, blob_key, url, prompt, style, provider, model, status, version, parent_asset_id
		      FROM assets WHERE project_id=$1`
		args := []any{projectID}
		if status != "" {
			q += ` AND status=$2`
			args = append(args, status)
		}
		q += ` ORDER BY created_at DESC`
		rows, err = a.db.WithContext(ctx).Raw(q, args...).Rows()
	} else {
		q := `SELECT a.id, a.shot_id, a.type, a.blob_key, a.url, a.prompt, a.style, a.provider, a.model, a.status, a.version, a.parent_asset_id
		      FROM assets a
		      JOIN todos t ON a.todo_id = t.id
		      WHERE a.project_id=$1 AND t.plan_id=$2`
		args := []any{projectID, planID}
		if status != "" {
			q += ` AND a.status=$3`
			args = append(args, status)
		}
		q += ` ORDER BY a.created_at DESC`
		rows, err = a.db.WithContext(ctx).Raw(q, args...).Rows()
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		var id, shotID, typ, blobKey, url, prompt, style, provider, model, st, parent string
		var version int
		if err := rows.Scan(&id, &shotID, &typ, &blobKey, &url, &prompt, &style, &provider, &model, &st, &version, &parent); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"id": id, "shotId": shotID, "type": typ, "blobKey": blobKey, "url": url,
			"prompt": prompt, "style": style, "provider": provider, "model": model,
			"status": st, "version": version, "parentAssetId": parent,
		})
	}
	return out, rows.Err()
}
