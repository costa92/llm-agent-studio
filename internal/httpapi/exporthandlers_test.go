package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"gorm.io/gorm"

	"github.com/costa92/llm-agent-studio/internal/blob"
	"github.com/costa92/llm-agent-studio/internal/exports"
	"github.com/costa92/llm-agent-studio/internal/project"
	"github.com/costa92/llm-agent-studio/internal/storage"
)

// TestExportContentDisposition is a pure-function table test (no DB): asserts the
// Content-Disposition header carries a clean quoted ASCII filename (no stray quote)
// AND an RFC 5987 filename* whose percent-decode round-trips to the UTF-8 original.
func TestExportContentDisposition(t *testing.T) {
	cases := []struct {
		name      string
		projName  string
		ext       string
		wantASCII string // expected quoted ASCII filename value
		wantUTF8  string // expected round-tripped filename* value
	}{
		{"plain ascii", "My Book", ".pdf", "My Book.pdf", "My Book.pdf"},
		{"embedded quote", `a"b`, ".zip", "a_b.zip", `a"b.zip`},
		{"chinese", "绘本", ".epub", "export.epub", "绘本.epub"},
		{"chinese mixed", "绘本v2", ".pdf", "__v2.pdf", "绘本v2.pdf"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := exportContentDisposition(c.projName, c.ext)

			// Extract the quoted ASCII filename="...".
			ascii := extractDispParam(t, got, `filename="`, `"`)
			if ascii != c.wantASCII {
				t.Fatalf("ascii filename: want %q, got %q (header=%q)", c.wantASCII, ascii, got)
			}
			if strings.ContainsAny(ascii, "\"\\") {
				t.Fatalf("ascii filename must not contain a raw quote/backslash, got %q", ascii)
			}
			for _, r := range ascii {
				if r > 0x7e || r < 0x20 {
					t.Fatalf("ascii filename must be printable ASCII, got rune %q in %q", r, ascii)
				}
			}

			// Extract filename*=UTF-8''<pct> and decode it.
			const marker = "filename*=UTF-8''"
			idx := strings.Index(got, marker)
			if idx < 0 {
				t.Fatalf("header missing filename* param: %q", got)
			}
			pct := got[idx+len(marker):]
			decoded, err := url.PathUnescape(pct)
			if err != nil {
				t.Fatalf("filename* not valid percent-encoding: %v (raw=%q)", err, pct)
			}
			if decoded != c.wantUTF8 {
				t.Fatalf("filename* round-trip: want %q, got %q", c.wantUTF8, decoded)
			}
		})
	}
}

// extractDispParam pulls the substring between open and closeDelim delimiters.
func extractDispParam(t *testing.T, s, open, closeDelim string) string {
	t.Helper()
	i := strings.Index(s, open)
	if i < 0 {
		t.Fatalf("param %q not found in %q", open, s)
	}
	rest := s[i+len(open):]
	j := strings.Index(rest, closeDelim)
	if j < 0 {
		t.Fatalf("unterminated param %q in %q", open, s)
	}
	return rest[:j]
}

// openFreshExport creates a brand-new database (the shared one carries dirty rows
// that trip unique indexes), migrates it, and returns its pool + gorm handle. The
// DB is dropped on cleanup. Skips when no PG URL is set. Mirrors exports.openFresh.
func openFreshExport(t *testing.T) (*pgxpool.Pool, *gorm.DB) {
	t.Helper()
	base := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if base == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run export handler tests")
	}
	ctx := context.Background()

	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse PG URL: %v", err)
	}
	dbName := "exporth_test_" + randHex12()

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
	t.Logf("fresh export-handler db: %s", dbName)
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

func randHex12() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func newSeedID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// seedExportBook inserts a project (given kind) + plan + shots + accepted image
// assets, returning (projectID, planID). nAcceptedImages accepted image assets are
// attached to the first shots. Mirrors exports.seedBook's DDL.
func seedExportBook(t *testing.T, pool *pgxpool.Pool, org, kind string, nShots, nAcceptedImages int) (string, string) {
	t.Helper()
	ctx := context.Background()
	projID := newSeedID()
	planID := newSeedID()
	if _, err := pool.Exec(ctx,
		`INSERT INTO projects (id, org_id, name, created_by, kind, status)
		 VALUES ($1,$2,'My Book','u',$3,'draft')`, projID, org, kind); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO plans (id, project_id, status) VALUES ($1,$2,'created')`, planID, projID); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	shotIDs := make([]string, nShots)
	for i := 0; i < nShots; i++ {
		shotTodo := newSeedID()
		if _, err := pool.Exec(ctx,
			`INSERT INTO todos (id, project_id, plan_id, type, status) VALUES ($1,$2,$3,'shot','done')`,
			shotTodo, projID, planID); err != nil {
			t.Fatalf("seed shot todo: %v", err)
		}
		sid := newSeedID()
		shotIDs[i] = sid
		if _, err := pool.Exec(ctx,
			`INSERT INTO shots (id, project_id, script_id, todo_id, shot_no, action, ordering)
			 VALUES ($1,$2,'',$3,$4,$5,$6)`,
			sid, projID, shotTodo, i+1, "narration "+sid[:6], i); err != nil {
			t.Fatalf("seed shot: %v", err)
		}
	}
	for i := 0; i < nAcceptedImages && i < nShots; i++ {
		assetTodo := newSeedID()
		if _, err := pool.Exec(ctx,
			`INSERT INTO todos (id, project_id, plan_id, type, status) VALUES ($1,$2,$3,'image','done')`,
			assetTodo, projID, planID); err != nil {
			t.Fatalf("seed asset todo: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO assets (id, project_id, shot_id, todo_id, type, blob_key, status, version)
			 VALUES ($1,$2,$3,$4,'image',$5,'accepted',1)`,
			newSeedID(), projID, shotIDs[i], assetTodo, "exports-test/img"+newSeedID()[:6]+".png"); err != nil {
			t.Fatalf("seed asset: %v", err)
		}
	}
	return projID, planID
}

// fakeBlobRouter satisfies BlobRouter, routing everything at one in-memory Fake.
// The Fake exposes only Get(key), not ReadKey(ctx,key), so exportContentHandler's
// ctxReader probe misses and it falls to the SignedURL→302 rung.
type fakeBlobRouter struct{ fake *blob.Fake }

func (f *fakeBlobRouter) BlobStoreFor(context.Context, string) (blob.BlobStore, error) {
	return f.fake, nil
}
func (f *fakeBlobRouter) BlobStoreForMode(context.Context, string, string) (blob.BlobStore, error) {
	return f.fake, nil
}
func (f *fakeBlobRouter) BlobStoreForConfigID(context.Context, string, string) (blob.BlobStore, error) {
	return f.fake, nil
}
func (f *fakeBlobRouter) ConfigIDForMode(context.Context, string, string) (string, error) {
	return "builtin", nil
}
func (f *fakeBlobRouter) ResolveWriteTarget(context.Context, string, string) (blob.BlobStore, string, error) {
	return f.fake, "builtin", nil
}

func newExportReq(method, target, body string) *http.Request {
	var rdr *strings.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	} else {
		rdr = strings.NewReader("")
	}
	return httptest.NewRequest(method, target, rdr)
}

// TestCreateExportAnyKind201: post-m23 the export gate no longer keys on project
// kind (every project is custom). A non-picturebook project with a ready run
// enqueues (201) instead of the old 400.
func TestCreateExportAnyKind201(t *testing.T) {
	pool, db := openFreshExport(t)
	projID, _ := seedExportBook(t, pool, "org-std", "custom", 3, 3)

	h := createExportHandler(project.New(db), exports.New(db), exports.NewBookData(db))
	req := newExportReq(http.MethodPost, "/api/projects/"+projID+"/exports", `{"format":"pdf"}`)
	req.SetPathValue("id", projID)
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("any-kind export: want 201, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestCreateExportNotReady409: a picturebook with no accepted images is below the
// 成书 threshold → 409.
func TestCreateExportNotReady409(t *testing.T) {
	pool, db := openFreshExport(t)
	projID, _ := seedExportBook(t, pool, "org-nr", "picturebook", 3, 0)

	h := createExportHandler(project.New(db), exports.New(db), exports.NewBookData(db))
	req := newExportReq(http.MethodPost, "/api/projects/"+projID+"/exports", `{"format":"zip"}`)
	req.SetPathValue("id", projID)
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("not-ready export: want 409, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestCreateExportReady201: a ready picturebook enqueues a pending job; the
// resolved planId is persisted.
func TestCreateExportReady201(t *testing.T) {
	pool, db := openFreshExport(t)
	// 3 shots → 1 content page → threshold ceil(1/2)=1 accepted image.
	projID, planID := seedExportBook(t, pool, "org-rdy", "picturebook", 3, 2)

	store := exports.New(db)
	h := createExportHandler(project.New(db), store, exports.NewBookData(db))
	req := newExportReq(http.MethodPost, "/api/projects/"+projID+"/exports", `{"format":"zip"}`)
	req.SetPathValue("id", projID)
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("ready export: want 201, got %d (%s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		JobID string `json:"jobId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.JobID == "" {
		t.Fatalf("ready export: empty jobId")
	}
	job, err := store.Get(context.Background(), resp.JobID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if job.Status != "pending" {
		t.Fatalf("new job should be pending, got %q", job.Status)
	}
	if job.PlanID != planID {
		t.Fatalf("job should persist resolved planId %q, got %q", planID, job.PlanID)
	}
	if job.Format != "zip" {
		t.Fatalf("job format want zip, got %q", job.Format)
	}
}

// TestCreateExportOmittedPlanIdResolvesLatest: omitting planId resolves to the
// newest plan (ListPlans is created_at DESC, [0] is newest).
func TestCreateExportOmittedPlanIdResolvesLatest(t *testing.T) {
	pool, db := openFreshExport(t)
	projID, oldPlan := seedExportBook(t, pool, "org-latest", "picturebook", 3, 2)

	// Seed a second, newer plan with its own ready book content under the SAME
	// project so IsBookReady passes for whichever plan resolves.
	ctx := context.Background()
	newPlan := newSeedID()
	if _, err := pool.Exec(ctx,
		`INSERT INTO plans (id, project_id, status, created_at) VALUES ($1,$2,'created', now() + interval '1 minute')`,
		newPlan, projID); err != nil {
		t.Fatalf("seed newer plan: %v", err)
	}
	// Attach ready content (3 shots + 2 accepted images) to the newer plan.
	shotIDs := make([]string, 3)
	for i := 0; i < 3; i++ {
		shotTodo := newSeedID()
		if _, err := pool.Exec(ctx,
			`INSERT INTO todos (id, project_id, plan_id, type, status) VALUES ($1,$2,$3,'shot','done')`,
			shotTodo, projID, newPlan); err != nil {
			t.Fatalf("seed newplan shot todo: %v", err)
		}
		sid := newSeedID()
		shotIDs[i] = sid
		if _, err := pool.Exec(ctx,
			`INSERT INTO shots (id, project_id, script_id, todo_id, shot_no, action, ordering)
			 VALUES ($1,$2,'',$3,$4,$5,$6)`,
			sid, projID, shotTodo, i+1, "newplan narration", i); err != nil {
			t.Fatalf("seed newplan shot: %v", err)
		}
	}
	for i := 0; i < 2; i++ {
		assetTodo := newSeedID()
		if _, err := pool.Exec(ctx,
			`INSERT INTO todos (id, project_id, plan_id, type, status) VALUES ($1,$2,$3,'image','done')`,
			assetTodo, projID, newPlan); err != nil {
			t.Fatalf("seed newplan asset todo: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO assets (id, project_id, shot_id, todo_id, type, blob_key, status, version)
			 VALUES ($1,$2,$3,$4,'image',$5,'accepted',1)`,
			newSeedID(), projID, shotIDs[i], assetTodo, "exports-test/np"+newSeedID()[:6]+".png"); err != nil {
			t.Fatalf("seed newplan asset: %v", err)
		}
	}

	store := exports.New(db)
	h := createExportHandler(project.New(db), store, exports.NewBookData(db))
	req := newExportReq(http.MethodPost, "/api/projects/"+projID+"/exports", `{"format":"pdf"}`)
	req.SetPathValue("id", projID)
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("omitted-planId export: want 201, got %d (%s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		JobID string `json:"jobId"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	job, err := store.Get(ctx, resp.JobID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if job.PlanID != newPlan {
		t.Fatalf("omitted planId should resolve to newest plan %q, got %q (old=%q)", newPlan, job.PlanID, oldPlan)
	}
}

// TestGetExportCrossProject404: a jobId belonging to project B, fetched under
// project A's path, returns 404 (the ProjectID==id guard).
func TestGetExportCrossProject404(t *testing.T) {
	pool, db := openFreshExport(t)
	projA, _ := seedExportBook(t, pool, "org-x", "picturebook", 3, 2)
	projB, planB := seedExportBook(t, pool, "org-x", "picturebook", 3, 2)

	store := exports.New(db)
	jobB, err := store.Create(context.Background(), projB, planB, "zip")
	if err != nil {
		t.Fatalf("create jobB: %v", err)
	}

	h := getExportHandler(store)
	req := newExportReq(http.MethodGet, "/api/projects/"+projA+"/exports/"+jobB.ID, "")
	req.SetPathValue("id", projA)
	req.SetPathValue("jobId", jobB.ID)
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-project job fetch: want 404, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestExportContentNotDone404: a job that is not done cannot be downloaded.
func TestExportContentNotDone404(t *testing.T) {
	pool, db := openFreshExport(t)
	projID, planID := seedExportBook(t, pool, "org-c", "picturebook", 3, 2)

	store := exports.New(db)
	job, err := store.Create(context.Background(), projID, planID, "zip")
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	router := &fakeBlobRouter{fake: blob.NewFake()}
	h := exportContentHandler(store, router, project.New(db))
	req := newExportReq(http.MethodGet, "/api/exports/"+job.ID+"/content", "")
	req.SetPathValue("id", job.ID)
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("not-done content: want 404, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestExportContentDone302: a done job redirects (302) to the signed URL and sets
// the attachment Content-Disposition (Fake has no ctxReader → SignedURL rung).
func TestExportContentDone302(t *testing.T) {
	pool, db := openFreshExport(t)
	projID, planID := seedExportBook(t, pool, "org-d", "picturebook", 3, 2)

	store := exports.New(db)
	job, err := store.Create(context.Background(), projID, planID, "pdf")
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	// Claim → MarkDone to reach the terminal 'done' state with a blob key.
	if _, _, err := store.Claim(context.Background(), "t", time.Minute); err != nil {
		t.Fatalf("claim: %v", err)
	}
	blobKey := "exports/" + projID + "/" + job.ID + ".pdf"
	if err := store.MarkDone(context.Background(), job.ID, blobKey, "builtin", 1234); err != nil {
		t.Fatalf("mark done: %v", err)
	}

	// Put the artifact bytes so the Fake's SignedURL rung resolves (key must exist).
	fake := blob.NewFake()
	if err := fake.Put(context.Background(), blobKey, strings.NewReader("%PDF-1.4 fake"), "application/pdf"); err != nil {
		t.Fatalf("put artifact: %v", err)
	}
	router := &fakeBlobRouter{fake: fake}
	h := exportContentHandler(store, router, project.New(db))
	req := newExportReq(http.MethodGet, "/api/exports/"+job.ID+"/content", "")
	req.SetPathValue("id", job.ID)
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("done content: want 302, got %d (%s)", rec.Code, rec.Body.String())
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") || !strings.Contains(cd, ".pdf") {
		t.Fatalf("done content: Content-Disposition want attachment+.pdf, got %q", cd)
	}
}
