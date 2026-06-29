package exports

// runner 是 export_jobs 队列的消费者：claim 一个待导出任务 → 读分镜+已审核资产 →
// 装订成页（picturebook.Assemble）→ 按 job.Format 选渲染器产出 zip/pdf/epub 字节 →
// 落到写入后端（ResolveWriteTarget）→ MarkDone。它只编排，不持有具体 studiosvc 类型：
// 依赖 bookData / projectInfo / blobRouter 三个小接口，便于单测替身。
//
// 读字节阶梯（spec R1）：blob.BlobStore 接口本身不暴露按 key 读字节的方法，不同后端
// 暴露读的方式不同，readBytes 按 github → localfs → fake → 预签名 http.Get 四级依次
// 尝试。单张资产读失败只让该页降级（图缺 → 占位/空），不致整单失败；唯有渲染本身报错
// 才 MarkFailed。单 plan 收口（spec R2）：始终用 job.PlanID 读取，不做项目级聚合。

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"gorm.io/gorm"

	"github.com/costa92/llm-agent-studio/internal/blob"
	"github.com/costa92/llm-agent-studio/internal/picturebook"
)

// signedURLTTL 是读字节阶梯第 4 级预签名 URL 的有效期（仅 s3/oss 真后端走到这一级）。
const signedURLTTL = 10 * time.Minute

// bookData 提供某 plan 装订成书所需的输入（分镜 + 已审核资产）。
// 生产实现 = 本包内 gorm 支撑的 BookData；测试实现 = 替身。
type bookData interface {
	Shots(ctx context.Context, projectID, planID string) ([]picturebook.Shot, error)
	AcceptedAssets(ctx context.Context, projectID, planID string) ([]picturebook.Asset, error)
}

// projectInfo 提供项目名 + org + 写入目标后端 id + 当前存储 mode。
// mode 供资产 StorageConfigID 为空（旧行/内置默认）时回落到按 mode 解析读字节后端。
type projectInfo interface {
	Info(ctx context.Context, projectID string) (name, orgID, storageConfigID, mode string, err error)
}

// blobRouter 是 runner 需要的 *storagerouter.Router 子集。
type blobRouter interface {
	BlobStoreForMode(ctx context.Context, orgID, mode string) (blob.BlobStore, error)
	BlobStoreForConfigID(ctx context.Context, orgID, configID string) (blob.BlobStore, error)
	ResolveWriteTarget(ctx context.Context, orgID, projConfigID string) (blob.BlobStore, string, error)
}

// renderers 把 job.Format 映射到对应渲染器（picturebook 的三个包级函数）。
var renderers = map[string]picturebook.Renderer{
	"zip":  picturebook.RenderZip,
	"pdf":  picturebook.RenderPDF,
	"epub": picturebook.RenderEPUB,
}

// RunnerConfig 配置 Runner。零值字段由 NewRunner 填默认。
type RunnerConfig struct {
	WorkerID    string        // 租约持有者标识
	LeaseTTL    time.Duration // Claim 租约时长（默认 2m）
	MaxAttempts int           // MarkFailed 上限（默认 3）
	Backoff     time.Duration // 重试退避（默认 30s）
	CallTimeout time.Duration // 单任务处理超时；<=0 不限
	Logger      *slog.Logger  // nil → slog.Default()
}

// Runner 消费 export_jobs 队列。
type Runner struct {
	store    *Store
	data     bookData
	projects projectInfo
	router   blobRouter

	workerID    string
	leaseTTL    time.Duration
	maxAttempts int
	backoff     time.Duration
	callTimeout time.Duration
	log         *slog.Logger
}

// NewRunner 组装一个 Runner。
func NewRunner(store *Store, data bookData, projects projectInfo, router blobRouter, cfg RunnerConfig) *Runner {
	if cfg.WorkerID == "" {
		cfg.WorkerID = "export-runner"
	}
	if cfg.LeaseTTL <= 0 {
		cfg.LeaseTTL = 2 * time.Minute
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.Backoff < 0 {
		cfg.Backoff = 0
	}
	if cfg.Backoff == 0 {
		cfg.Backoff = 30 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Runner{
		store:       store,
		data:        data,
		projects:    projects,
		router:      router,
		workerID:    cfg.WorkerID,
		leaseTTL:    cfg.LeaseTTL,
		maxAttempts: cfg.MaxAttempts,
		backoff:     cfg.Backoff,
		callTimeout: cfg.CallTimeout,
		log:         cfg.Logger,
	}
}

// RunOnce claim 并处理恰好一个任务。返回 (true,nil) 处理了一个，(false,nil) 队列为空。
func (r *Runner) RunOnce(ctx context.Context) (bool, error) {
	job, ok, err := r.store.Claim(ctx, r.workerID, r.leaseTTL)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}

	// 单任务超时收口，避免一个卡死的读字节拖垮整个 runner（租约到期后会被 Reap）。
	jobCtx := ctx
	if r.callTimeout > 0 {
		var cancel context.CancelFunc
		jobCtx, cancel = context.WithTimeout(ctx, r.callTimeout)
		defer cancel()
	}

	r.process(jobCtx, job)
	return true, nil
}

// process 跑完一个 claimed 任务的全部阶段，自行决定 MarkDone / MarkFailed。
func (r *Runner) process(ctx context.Context, job ExportJob) {
	name, orgID, projConfigID, mode, err := r.projects.Info(ctx, job.ProjectID)
	if err != nil {
		r.fail(ctx, job.ID, fmt.Errorf("resolve project %s: %w", job.ProjectID, err))
		return
	}

	// R2：始终按 job.PlanID 读取，单 plan 收口，绝不做项目级聚合。
	shots, err := r.data.Shots(ctx, job.ProjectID, job.PlanID)
	if err != nil {
		r.fail(ctx, job.ID, fmt.Errorf("load shots: %w", err))
		return
	}
	assetList, err := r.data.AcceptedAssets(ctx, job.ProjectID, job.PlanID)
	if err != nil {
		r.fail(ctx, job.ID, fmt.Errorf("load accepted assets: %w", err))
		return
	}

	// 成书阈值兜底（handler 已守卫，runner 再防一层）。
	if !picturebook.IsBookReady(shots, assetList) {
		r.fail(ctx, job.ID, errors.New("book not ready (insufficient accepted assets)"))
		return
	}

	render, ok := renderers[job.Format]
	if !ok {
		r.fail(ctx, job.ID, fmt.Errorf("unsupported export format %q", job.Format))
		return
	}

	// 资产按 ID 索引，供页拉字节（Page 只带 assetID）。
	byID := make(map[string]picturebook.Asset, len(assetList))
	for _, a := range assetList {
		byID[a.ID] = a
	}

	pages := picturebook.Assemble(name, shots, assetList)
	pageBytes := make([]picturebook.PageBytes, len(pages))
	for i, p := range pages {
		var pb picturebook.PageBytes
		if img, ok := byID[p.ImageAssetID]; ok {
			pb.ImageBytes, pb.ImageMIME = r.assetBytes(ctx, orgID, mode, img)
		}
		if au, ok := byID[p.AudioAssetID]; ok {
			pb.AudioBytes, pb.AudioMIME = r.assetBytes(ctx, orgID, mode, au)
		}
		pageBytes[i] = pb
	}

	data, contentType, err := render(name, pages, pageBytes)
	if err != nil {
		r.fail(ctx, job.ID, fmt.Errorf("render %s: %w", job.Format, err))
		return
	}

	bs, cfgID, _ := r.router.ResolveWriteTarget(ctx, orgID, projConfigID)
	blobKey := fmt.Sprintf("exports/%s/%s%s", job.ProjectID, job.ID, extForFormat(job.Format))
	if err := bs.Put(ctx, blobKey, bytes.NewReader(data), contentType); err != nil {
		r.fail(ctx, job.ID, fmt.Errorf("put export artifact: %w", err))
		return
	}

	if err := r.store.MarkDone(ctx, job.ID, blobKey, cfgID, int64(len(data))); err != nil {
		if errors.Is(err, ErrNotRunning) {
			// 竞态输家：任务已被 Reap/抢占，落库结果作废，记录后退出。
			r.log.Info("export job left running before MarkDone (race loser)", "job", job.ID)
			return
		}
		r.log.Error("export MarkDone failed", "job", job.ID, "err", err)
	}
}

// assetBytes 解析单张资产的字节 + content-type，失败则降级返回空（不致整单失败）。
// 资产 StorageConfigID 非空 → 按 id 解析后端；为空（旧行/内置默认）→ 按项目 mode 回落。
func (r *Runner) assetBytes(ctx context.Context, orgID, mode string, a picturebook.Asset) ([]byte, string) {
	if a.ID == "" || a.BlobKey == "" {
		return nil, ""
	}
	var (
		bs  blob.BlobStore
		err error
	)
	if a.StorageConfigID == "" {
		bs, err = r.router.BlobStoreForMode(ctx, orgID, mode)
	} else {
		bs, err = r.router.BlobStoreForConfigID(ctx, orgID, a.StorageConfigID)
	}
	if err != nil || bs == nil {
		r.log.Warn("export: resolve blob store failed; page degraded", "asset", a.ID, "err", err)
		return nil, ""
	}
	data, ct, err := readBytes(ctx, bs, a.BlobKey)
	if err != nil {
		r.log.Warn("export: read asset bytes failed; page degraded", "asset", a.ID, "key", a.BlobKey, "err", err)
		return nil, ""
	}
	return data, ct
}

// readBytes 是读字节阶梯（spec R1）的唯一实现：blob.BlobStore 接口不暴露按 key 读字节，
// 不同后端暴露读的方式不同，按下列顺序逐级类型断言/回落：
//  1. ReadKey(ctx, key) → github 后端。
//  2. ReadKey(key)（无 ctx）→ localfs 后端。
//  3. Get(key) ([]byte,string,bool) → Fake 后端（ok==false 即未找到）。
//  4. 兜底：SignedURL + http.Get → s3/oss 真预签名。
//
// localfs 的 SignedURL 是需要活服务的 HMAC 后端 URL，故 localfs 必须在第 2 级被截获，
// 绝不能落到第 4 级。
func readBytes(ctx context.Context, bs blob.BlobStore, key string) ([]byte, string, error) {
	// 1) github：带 ctx 的 ReadKey。
	if rdr, ok := bs.(interface {
		ReadKey(ctx context.Context, key string) ([]byte, string, error)
	}); ok {
		return rdr.ReadKey(ctx, key)
	}
	// 2) localfs：无 ctx 的 ReadKey。
	if rdr, ok := bs.(interface {
		ReadKey(key string) ([]byte, string, error)
	}); ok {
		return rdr.ReadKey(key)
	}
	// 3) Fake：Get。
	if rdr, ok := bs.(interface {
		Get(key string) ([]byte, string, bool)
	}); ok {
		data, ct, found := rdr.Get(key)
		if !found {
			return nil, "", fmt.Errorf("exports: blob key %q not found", key)
		}
		return data, ct, nil
	}
	// 4) 兜底：预签名 URL + http.Get（s3/oss 真后端）。
	signed, err := bs.SignedURL(ctx, key, signedURLTTL)
	if err != nil {
		return nil, "", fmt.Errorf("exports: sign url for %q: %w", key, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, signed, nil)
	if err != nil {
		return nil, "", fmt.Errorf("exports: build read request for %q: %w", key, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("exports: http get %q: %w", key, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("exports: http get %q: status %d", key, resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("exports: read body for %q: %w", key, err)
	}
	return data, resp.Header.Get("Content-Type"), nil
}

// fail 包一层 MarkFailed，把 ErrNotRunning（竞态输家）当作良性。
func (r *Runner) fail(ctx context.Context, id string, cause error) {
	if err := r.store.MarkFailed(ctx, id, cause.Error(), r.maxAttempts, r.backoff); err != nil {
		if errors.Is(err, ErrNotRunning) {
			r.log.Info("export job left running before MarkFailed (race loser)", "job", id, "cause", cause)
			return
		}
		r.log.Error("export MarkFailed failed", "job", id, "cause", cause, "err", err)
	}
}

// Run 循环 RunOnce，空闲或出错时 sleep pollInterval，ctx 取消即退出（优雅 drain）。
// 不在此跑 reaper（Store.Reap 由 main.go 的独立 ticker 调度，T5 接线）。
func (r *Runner) Run(ctx context.Context, pollInterval time.Duration) {
	for {
		if ctx.Err() != nil {
			return
		}
		ran, err := r.RunOnce(ctx)
		if err != nil {
			r.log.Error("export runner claim failed", "worker", r.workerID, "err", err)
			ran = false
		}
		if !ran {
			select {
			case <-ctx.Done():
				return
			case <-time.After(pollInterval):
			}
		}
	}
}

// extForFormat 把导出格式映射到文件扩展名。
func extForFormat(format string) string {
	switch format {
	case "pdf":
		return ".pdf"
	case "epub":
		return ".epub"
	case "zip":
		return ".zip"
	default:
		return ""
	}
}

// BookData 是 bookData 的 gorm 支撑实现，镜像 studiosvc/artifacts.go 的 per-plan 查询，
// 但 AcceptedAssets 额外 SELECT a.storage_config_id（artifacts.Assets 不返回此列——这是
// 本任务收口的 🟡 缺口）并过滤 status='accepted'。
type BookData struct{ db *gorm.DB }

// NewBookData 构造 BookData。
func NewBookData(db *gorm.DB) *BookData { return &BookData{db: db} }

// Shots 取某 plan 的分镜，按 ordering 升序（镜像 artifacts.Shots per-plan 路径）。
func (b *BookData) Shots(ctx context.Context, projectID, planID string) ([]picturebook.Shot, error) {
	rows, err := b.db.WithContext(ctx).Raw(
		`SELECT s.id, s.shot_no, s.action, s.ordering FROM shots s
		 JOIN todos t ON s.todo_id = t.id
		 WHERE s.project_id=$1 AND t.plan_id=$2
		 ORDER BY s.ordering ASC`,
		projectID, planID).Rows()
	if err != nil {
		return nil, fmt.Errorf("exports: load shots: %w", err)
	}
	defer rows.Close()
	var out []picturebook.Shot
	for rows.Next() {
		var (
			id       string
			shotNo   int
			action   string
			ordering int
		)
		if err := rows.Scan(&id, &shotNo, &action, &ordering); err != nil {
			return nil, fmt.Errorf("exports: scan shot: %w", err)
		}
		out = append(out, picturebook.Shot{
			ID:       id,
			ShotNo:   strconv.Itoa(shotNo),
			Action:   action,
			Ordering: ordering,
		})
	}
	return out, rows.Err()
}

// AcceptedAssets 取某 plan 的 accepted 资产（含 storage_config_id，供读字节阶梯解析后端）。
func (b *BookData) AcceptedAssets(ctx context.Context, projectID, planID string) ([]picturebook.Asset, error) {
	rows, err := b.db.WithContext(ctx).Raw(
		`SELECT a.id, a.shot_id, a.type, a.blob_key, a.status, a.version,
		        a.prompt, a.provider, a.model, a.storage_config_id
		 FROM assets a
		 JOIN todos t ON a.todo_id = t.id
		 WHERE a.project_id=$1 AND t.plan_id=$2 AND a.status='accepted'
		 ORDER BY a.created_at DESC`,
		projectID, planID).Rows()
	if err != nil {
		return nil, fmt.Errorf("exports: load accepted assets: %w", err)
	}
	defer rows.Close()
	var out []picturebook.Asset
	for rows.Next() {
		var (
			id, shotID, typ, blobKey, status string
			version                          int
			prompt, provider, model          string
			storageConfigID                  sql.NullString
		)
		if err := rows.Scan(&id, &shotID, &typ, &blobKey, &status, &version,
			&prompt, &provider, &model, &storageConfigID); err != nil {
			return nil, fmt.Errorf("exports: scan asset: %w", err)
		}
		out = append(out, picturebook.Asset{
			ID:              id,
			ShotID:          shotID,
			Type:            typ,
			BlobKey:         blobKey,
			Status:          status,
			Version:         version,
			Prompt:          prompt,
			Provider:        provider,
			Model:           model,
			StorageConfigID: storageConfigID.String,
		})
	}
	return out, rows.Err()
}

// ProjectInfo 是 projectInfo 的 gorm 支撑实现：读项目名/org/写入后端 id/存储 mode。
type ProjectInfo struct{ db *gorm.DB }

// NewProjectInfo 构造 ProjectInfo。
func NewProjectInfo(db *gorm.DB) *ProjectInfo { return &ProjectInfo{db: db} }

// Info 返回 (name, orgID, storageConfigID, mode)。storageConfigID = 项目写入覆盖；
// mode = 项目存储 mode（资产 StorageConfigID 为空时读字节回落用）。
func (p *ProjectInfo) Info(ctx context.Context, projectID string) (name, orgID, storageConfigID, mode string, err error) {
	row := p.db.WithContext(ctx).Raw(
		`SELECT name, org_id, storage_config_id, storage_mode FROM projects WHERE id=$1`,
		projectID).Row()
	if err = row.Scan(&name, &orgID, &storageConfigID, &mode); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", "", "", fmt.Errorf("exports: project %s: %w", projectID, ErrNotFound)
		}
		return "", "", "", "", fmt.Errorf("exports: load project %s: %w", projectID, err)
	}
	return name, orgID, storageConfigID, mode, nil
}
