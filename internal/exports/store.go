// Package exports owns the export_jobs queue: an INDEPENDENT async queue for the
// picturebook 成书导出 feature. Exporting is a read-only consumption of accepted
// assets (render → blob), so it must not pollute the todos run-queue / run
// lifecycle. It reuses the worker's validated claim/lease/reaper PATTERN (status
// + locked_by/locked_until + next_run_at, FOR UPDATE SKIP LOCKED) on its own
// table.
//
// State machine:
//
//	pending ──claim──▶ running ──MarkDone──▶ done   (terminal)
//	   ▲                  │
//	   └──MarkFailed──────┤  attempts<max ──▶ pending (backoff next_run_at)
//	                      └  attempts≥max ──▶ failed  (terminal, error)
//	Reap: running & locked_until < now()-ttl ──▶ failed (stranded-lease backstop)
//
// MarkDone/MarkFailed guard on `status='running'` and arbitrate races via
// RowsAffected (a row that already left running is a no-op → ErrNotRunning),
// mirroring assets/todos store conventions. Store writes use raw `$N` SQL through
// the GORM handle (studio store 铁律: INSERT...RETURNING, no AutoMigrate, no
// gorm.Create), with sql.NullTime for the nullable locked_until column.
package exports

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// ErrNotFound is returned when an export_jobs row does not exist.
var ErrNotFound = errors.New("exports: not found")

// ErrNotRunning is returned by MarkDone/MarkFailed when the targeted job is not
// in the 'running' state (already terminal-stated, reclaimed, or never claimed).
// The caller is the race loser and should bow out benignly.
var ErrNotRunning = errors.New("exports: job not in running state")

// ExportJob is an export_jobs row.
type ExportJob struct {
	ID              string    `json:"id"`
	ProjectID       string    `json:"projectId"`
	PlanID          string    `json:"planId"`
	Format          string    `json:"format"`
	Status          string    `json:"status"`
	BlobKey         string    `json:"blobKey"`
	StorageConfigID string    `json:"storageConfigId"`
	SizeBytes       int64     `json:"sizeBytes"`
	Error           string    `json:"error"`
	Attempts        int       `json:"attempts"`
	LockedBy        string    `json:"lockedBy"`
	LockedUntil     time.Time `json:"lockedUntil"` // zero when NULL
	NextRunAt       time.Time `json:"nextRunAt"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// Store persists export jobs.
type Store struct{ db *gorm.DB }

// New builds a Store over the coexisting GORM handle.
func New(db *gorm.DB) *Store { return &Store{db: db} }

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

const jobCols = `id, project_id, plan_id, format, status, blob_key, storage_config_id, size_bytes, error, attempts, locked_by, locked_until, next_run_at, created_at, updated_at`

func scanJob(row interface{ Scan(...any) error }) (ExportJob, error) {
	var j ExportJob
	var lockedUntil sql.NullTime
	err := row.Scan(&j.ID, &j.ProjectID, &j.PlanID, &j.Format, &j.Status, &j.BlobKey,
		&j.StorageConfigID, &j.SizeBytes, &j.Error, &j.Attempts, &j.LockedBy, &lockedUntil,
		&j.NextRunAt, &j.CreatedAt, &j.UpdatedAt)
	if lockedUntil.Valid {
		j.LockedUntil = lockedUntil.Time
	}
	return j, err
}

// Create inserts a pending export job for (projectID, planID, format) and returns
// the full row (INSERT...RETURNING). The id is application-generated hex.
func (s *Store) Create(ctx context.Context, projectID, planID, format string) (ExportJob, error) {
	id := newID()
	j, err := scanJob(s.db.WithContext(ctx).Raw(
		`INSERT INTO export_jobs (id, project_id, plan_id, format, status)
		 VALUES ($1, $2, $3, $4, 'pending')
		 RETURNING `+jobCols,
		id, projectID, planID, format).Row())
	if err != nil {
		return ExportJob{}, fmt.Errorf("exports: create: %w", err)
	}
	return j, nil
}

// Claim atomically selects one due pending job (FOR UPDATE SKIP LOCKED so
// concurrent runners never claim the same row), flips it to running with a fresh
// DB-clock lease (locked_by/locked_until), and returns it. found=false (no error)
// when the queue has nothing claimable. leaseTTL bounds how long the lease is
// held before the reaper may reclaim a stranded job.
func (s *Store) Claim(ctx context.Context, workerID string, leaseTTL time.Duration) (ExportJob, bool, error) {
	leaseSecs := int(leaseTTL / time.Second)
	if leaseSecs <= 0 {
		leaseSecs = 120
	}
	j, err := scanJob(s.db.WithContext(ctx).Raw(`
		UPDATE export_jobs
		SET status='running', locked_by=$1,
		    locked_until = now() + make_interval(secs => $2), updated_at=now()
		WHERE id = (
			SELECT id FROM export_jobs
			WHERE status='pending' AND next_run_at <= now()
			ORDER BY next_run_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		RETURNING `+jobCols,
		workerID, leaseSecs).Row())
	if errors.Is(err, sql.ErrNoRows) {
		return ExportJob{}, false, nil
	}
	if err != nil {
		return ExportJob{}, false, fmt.Errorf("exports: claim: %w", err)
	}
	return j, true, nil
}

// MarkDone terminal-states a running job to done, recording the produced blob.
// Guarded on status='running' (RowsAffected==0 → ErrNotRunning) so a job that was
// reclaimed/reaped under us is a benign no-op for the loser.
func (s *Store) MarkDone(ctx context.Context, id, blobKey, storageConfigID string, size int64) error {
	res := s.db.WithContext(ctx).Exec(`
		UPDATE export_jobs
		SET status='done', blob_key=$2, storage_config_id=$3, size_bytes=$4,
		    error='', locked_by='', locked_until=NULL, updated_at=now()
		WHERE id=$1 AND status='running'`,
		id, blobKey, storageConfigID, size)
	if res.Error != nil {
		return fmt.Errorf("exports: mark done: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotRunning
	}
	return nil
}

// MarkFailed records a failure on a running job. attempts is incremented; while
// the new attempts count is below maxAttempts the job is rescheduled to pending
// with a backoff (next_run_at = now()+backoff), otherwise it is terminal-stated
// to failed. Guarded on status='running' (RowsAffected==0 → ErrNotRunning).
func (s *Store) MarkFailed(ctx context.Context, id, errMsg string, maxAttempts int, backoff time.Duration) error {
	backoffSecs := int(backoff / time.Second)
	if backoffSecs < 0 {
		backoffSecs = 0
	}
	res := s.db.WithContext(ctx).Exec(`
		UPDATE export_jobs
		SET attempts = attempts + 1,
		    error = $2,
		    status = CASE WHEN attempts + 1 >= $3 THEN 'failed' ELSE 'pending' END,
		    next_run_at = CASE WHEN attempts + 1 >= $3 THEN next_run_at
		                       ELSE now() + make_interval(secs => $4) END,
		    locked_by = '', locked_until = NULL, updated_at = now()
		WHERE id=$1 AND status='running'`,
		id, errMsg, maxAttempts, backoffSecs)
	if res.Error != nil {
		return fmt.Errorf("exports: mark failed: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotRunning
	}
	return nil
}

// Get returns an export job by id.
func (s *Store) Get(ctx context.Context, id string) (ExportJob, error) {
	j, err := scanJob(s.db.WithContext(ctx).Raw(
		`SELECT `+jobCols+` FROM export_jobs WHERE id=$1`, id).Row())
	if errors.Is(err, sql.ErrNoRows) {
		return ExportJob{}, ErrNotFound
	}
	if err != nil {
		return ExportJob{}, fmt.Errorf("exports: get: %w", err)
	}
	return j, nil
}

// ListByProject returns a project's export history, newest first.
func (s *Store) ListByProject(ctx context.Context, projectID string) ([]ExportJob, error) {
	rows, err := s.db.WithContext(ctx).Raw(
		`SELECT `+jobCols+` FROM export_jobs WHERE project_id=$1 ORDER BY created_at DESC`,
		projectID).Rows()
	if err != nil {
		return nil, fmt.Errorf("exports: list by project: %w", err)
	}
	defer rows.Close()
	var out []ExportJob
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("exports: scan: %w", err)
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// Reap terminal-states (→ failed) running jobs whose lease has been expired for
// longer than ttl: a crashed runner would otherwise strand the job forever
// (backstop, mirrors assets.ReapStaleSubmitted / worker reaper). Returns the
// number reaped.
func (s *Store) Reap(ctx context.Context, ttl time.Duration) (int, error) {
	ttlSecs := int(ttl / time.Second)
	if ttlSecs < 0 {
		ttlSecs = 0
	}
	res := s.db.WithContext(ctx).Exec(`
		UPDATE export_jobs
		SET status='failed', error='lease expired (reaped)',
		    locked_by='', locked_until=NULL, updated_at=now()
		WHERE status='running' AND locked_until IS NOT NULL
		  AND locked_until < now() - make_interval(secs => $1)`,
		ttlSecs)
	if res.Error != nil {
		return 0, fmt.Errorf("exports: reap: %w", res.Error)
	}
	return int(res.RowsAffected), nil
}
