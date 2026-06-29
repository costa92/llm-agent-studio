package exports

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"gorm.io/gorm"

	"github.com/costa92/llm-agent-studio/internal/blob"
	"github.com/costa92/llm-agent-studio/internal/storage"
)

// openFresh creates a brand-new database (the shared one carries dirty rows that
// trip unique indexes like assets_todo_uniq), migrates it, and returns its pool +
// gorm handle. The DB is dropped on cleanup. Skips when no PG URL is set.
func openFresh(t *testing.T) (*pgxpool.Pool, *gorm.DB) {
	t.Helper()
	base := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if base == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run exports runner tests")
	}
	ctx := context.Background()

	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse PG URL: %v", err)
	}
	dbName := "runner_test_" + newID()[:12]

	admin, err := pgxpool.New(ctx, base)
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+dbName); err != nil {
		admin.Close()
		t.Fatalf("create database %s: %v", dbName, err)
	}
	admin.Close()

	freshURL := *u
	freshURL.Path = "/" + dbName
	st, err := storage.Open(ctx, storage.Config{PGURL: freshURL.String()})
	if err != nil {
		t.Fatalf("open fresh db: %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		st.Close()
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() {
		st.Close()
		admin2, err := pgxpool.New(context.Background(), base)
		if err != nil {
			t.Logf("cleanup admin pool: %v", err)
			return
		}
		defer admin2.Close()
		if _, err := admin2.Exec(context.Background(), `DROP DATABASE IF EXISTS `+dbName+` WITH (FORCE)`); err != nil {
			t.Logf("drop database %s: %v", dbName, err)
		}
	})
	return st.Pool(), st.GORM()
}

// fakeRouter routes every read/write at the SAME in-memory Fake, so the read
// ladder hits the Fake.Get rung and writes land in Fake.Put.
type fakeRouter struct{ fake *blob.Fake }

func (f *fakeRouter) BlobStoreForMode(_ context.Context, _, _ string) (blob.BlobStore, error) {
	return f.fake, nil
}
func (f *fakeRouter) BlobStoreForConfigID(_ context.Context, _, _ string) (blob.BlobStore, error) {
	return f.fake, nil
}
func (f *fakeRouter) ResolveWriteTarget(_ context.Context, _, _ string) (blob.BlobStore, string, error) {
	return f.fake, "builtin", nil
}

// seedBook inserts a project (kind picturebook) + plan + shots + (optionally)
// accepted image assets, and returns (projectID, planID). Each shot gets its own
// shots-todo, each asset its own asset-todo (assets_todo_uniq forbids reusing a
// non-empty todo_id). imageKeys maps a shot index → its accepted image blob_key.
func seedBook(t *testing.T, pool *pgxpool.Pool, org string, nShots int, imageKeys map[int]string) (string, string) {
	t.Helper()
	ctx := context.Background()
	projID := newID()
	planID := newID()
	if _, err := pool.Exec(ctx,
		`INSERT INTO projects (id, org_id, name, created_by, kind, status)
		 VALUES ($1,$2,'My Book','u','picturebook','draft')`, projID, org); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO plans (id, project_id, status) VALUES ($1,$2,'created')`, planID, projID); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	shotIDs := make([]string, nShots)
	for i := 0; i < nShots; i++ {
		shotTodo := newID()
		if _, err := pool.Exec(ctx,
			`INSERT INTO todos (id, project_id, plan_id, type, status) VALUES ($1,$2,$3,'shot','done')`,
			shotTodo, projID, planID); err != nil {
			t.Fatalf("seed shot todo: %v", err)
		}
		sid := newID()
		shotIDs[i] = sid
		if _, err := pool.Exec(ctx,
			`INSERT INTO shots (id, project_id, script_id, todo_id, shot_no, action, ordering)
			 VALUES ($1,$2,'',$3,$4,$5,$6)`,
			sid, projID, shotTodo, i+1, "narration for shot "+sid[:6], i); err != nil {
			t.Fatalf("seed shot: %v", err)
		}
	}
	for shotIdx, key := range imageKeys {
		assetTodo := newID()
		if _, err := pool.Exec(ctx,
			`INSERT INTO todos (id, project_id, plan_id, type, status) VALUES ($1,$2,$3,'image','done')`,
			assetTodo, projID, planID); err != nil {
			t.Fatalf("seed asset todo: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO assets (id, project_id, shot_id, todo_id, type, blob_key, status, version)
			 VALUES ($1,$2,$3,$4,'image',$5,'accepted',1)`,
			newID(), projID, shotIDs[shotIdx], assetTodo, key); err != nil {
			t.Fatalf("seed asset: %v", err)
		}
	}
	return projID, planID
}

func newRunner(pool *pgxpool.Pool, db *gorm.DB, fake *blob.Fake) (*Runner, *Store) {
	store := New(db)
	return NewRunner(store, NewBookData(db), NewProjectInfo(db), &fakeRouter{fake: fake},
		RunnerConfig{WorkerID: "t", LeaseTTL: time.Minute, MaxAttempts: 3, Backoff: time.Minute}), store
}

// TestRunOnceZipHappyPath: a ready picturebook + a pending zip job → RunOnce
// renders, writes the artifact to the Fake, and marks the job done. The artifact
// bytes start with the zip magic "PK". The image bytes are read through the
// Fake.Get rung of the read ladder.
func TestRunOnceZipHappyPath(t *testing.T) {
	pool, db := openFresh(t)
	ctx := context.Background()
	fake := blob.NewFake()

	// Pre-Put the image bytes the accepted assets point at.
	imgKey0 := "exports-test/cover.png"
	imgKey1 := "exports-test/p1.png"
	pngMagic := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 'd', 'a', 't', 'a'}
	if err := fake.Put(ctx, imgKey0, bytesReader(pngMagic), "image/png"); err != nil {
		t.Fatalf("put img0: %v", err)
	}
	if err := fake.Put(ctx, imgKey1, bytesReader(pngMagic), "image/png"); err != nil {
		t.Fatalf("put img1: %v", err)
	}

	projID, planID := seedBook(t, pool, "org-happy", 3, map[int]string{0: imgKey0, 1: imgKey1})

	runner, store := newRunner(pool, db, fake)
	job, err := store.Create(ctx, projID, planID, "zip")
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	ran, err := runner.RunOnce(ctx)
	if err != nil || !ran {
		t.Fatalf("RunOnce: ran=%v err=%v", ran, err)
	}

	got, err := store.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if got.Status != "done" {
		t.Fatalf("job should be done, got %q (err=%q)", got.Status, got.Error)
	}
	if got.BlobKey == "" {
		t.Fatalf("done job should carry a blob_key")
	}
	if got.SizeBytes <= 0 {
		t.Fatalf("done job should carry size>0, got %d", got.SizeBytes)
	}
	artifact, _, ok := fake.Get(got.BlobKey)
	if !ok {
		t.Fatalf("artifact not found in fake at %q", got.BlobKey)
	}
	if len(artifact) < 2 || artifact[0] != 'P' || artifact[1] != 'K' {
		t.Fatalf("artifact is not a zip (no PK magic): %x", artifact[:min(4, len(artifact))])
	}
}

// TestRunOnceDegradesMissingBytes: an accepted asset whose blob_key is absent
// from the Fake → that page degrades (empty image) but the render still succeeds,
// so the job is done, NOT failed. Proves single-asset read failure ≠ job failure.
func TestRunOnceDegradesMissingBytes(t *testing.T) {
	pool, db := openFresh(t)
	ctx := context.Background()
	fake := blob.NewFake()

	// Two accepted images: one present, one whose key is NOT in the Fake.
	presentKey := "exports-test/present.png"
	missingKey := "exports-test/missing.png"
	if err := fake.Put(ctx, presentKey, bytesReader([]byte{0x89, 0x50, 0x4E, 0x47, 'x'}), "image/png"); err != nil {
		t.Fatalf("put present: %v", err)
	}
	projID, planID := seedBook(t, pool, "org-degrade", 3, map[int]string{0: presentKey, 1: missingKey})

	runner, store := newRunner(pool, db, fake)
	job, err := store.Create(ctx, projID, planID, "zip")
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	ran, err := runner.RunOnce(ctx)
	if err != nil || !ran {
		t.Fatalf("RunOnce: ran=%v err=%v", ran, err)
	}
	got, _ := store.Get(ctx, job.ID)
	if got.Status != "done" {
		t.Fatalf("degraded job should still be done, got %q (err=%q)", got.Status, got.Error)
	}
}

// TestRunOnceNotReadyFails: a book with no accepted images is below the成书
// threshold → IsBookReady false → MarkFailed. With MaxAttempts>1 the first
// failure reschedules to pending (attempts=1), so assert the failure was recorded.
func TestRunOnceNotReadyFails(t *testing.T) {
	pool, db := openFresh(t)
	ctx := context.Background()
	fake := blob.NewFake()

	projID, planID := seedBook(t, pool, "org-notready", 3, nil) // no accepted images

	runner, store := newRunner(pool, db, fake)
	job, err := store.Create(ctx, projID, planID, "zip")
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	ran, err := runner.RunOnce(ctx)
	if err != nil || !ran {
		t.Fatalf("RunOnce: ran=%v err=%v", ran, err)
	}
	got, _ := store.Get(ctx, job.ID)
	if got.Attempts != 1 {
		t.Fatalf("not-ready job should record one failed attempt, got attempts=%d status=%q", got.Attempts, got.Status)
	}
	if got.Error == "" {
		t.Fatalf("not-ready job should record an error message")
	}
	if got.BlobKey != "" {
		t.Fatalf("not-ready job must not produce an artifact, got blob_key=%q", got.BlobKey)
	}
}

func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }
