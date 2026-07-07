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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"gorm.io/gorm"

	"github.com/costa92/llm-agent-studio/internal/blob"
	"github.com/costa92/llm-agent-studio/internal/picturebook"
)

// signedURLTTL 是读字节阶梯第 4 级预签名 URL 的有效期（仅 s3/oss 真后端走到这一级）。
const signedURLTTL = 10 * time.Minute

// maxAssetBytes 是单张资产读字节的上限（防御无 Content-Length 响应 OOM）。
const maxAssetBytes = 64 << 20 // 64 MiB

// assetHTTPClient 是读字节阶梯第 4 级专用的 HTTP 客户端：显式 Timeout 独立于
// CallTimeout，保证 CallTimeout==0（默认）时预签名拉取仍有死线——慢/半开的存储端点
// 不会永久阻塞唯一的 Run goroutine、卡死整个导出队列（Reap 只改 DB 行，解不开 goroutine）。
// CheckRedirect 拒绝跳转到 loopback/私网/链路本地的目标（SSRF-via-redirect 纵深防御）。
var assetHTTPClient = &http.Client{
	Timeout: 60 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("exports: too many redirects")
		}
		return guardPublicHost(req.URL.Hostname())
	},
}

// guardPublicHost 拒绝解析到 loopback/私网/链路本地/未指定地址的主机（SSRF 纵深防御）。
func guardPublicHost(host string) error {
	if host == "" {
		return fmt.Errorf("exports: redirect to empty host refused")
	}
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		resolved, err := net.LookupIP(host)
		if err != nil {
			return fmt.Errorf("exports: resolve redirect host %q: %w", host, err)
		}
		ips = resolved
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
			ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("exports: redirect to non-public host %q refused", host)
		}
	}
	return nil
}

// bookData 提供某 plan 装订成作品所需的输入（分镜 + 导出可用资产 + LLM 成品文档）。
// 生产实现 = 本包内 gorm 支撑的 BookData；测试实现 = 替身。
type bookData interface {
	Shots(ctx context.Context, projectID, planID string) ([]picturebook.Shot, error)
	ExportAssets(ctx context.Context, projectID, planID string) ([]picturebook.Asset, error)
	StoryDoc(ctx context.Context, projectID, planID string) (title, story, lyrics string, err error)
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
	assetList, err := r.data.ExportAssets(ctx, job.ProjectID, job.PlanID)
	if err != nil {
		r.fail(ctx, job.ID, fmt.Errorf("load export assets: %w", err))
		return
	}

	// LLM 成品文档（title/story）——net-new：用于 intro/cover 页 + 全文档（无图）可导出判定。
	// 读失败不致命：降级到无 intro 页 + 仅按图判定就绪。
	storyTitle, story, _, sErr := r.data.StoryDoc(ctx, job.ProjectID, job.PlanID)
	if sErr != nil {
		r.log.Warn("export: load story doc failed; no intro page", "project", job.ProjectID, "plan", job.PlanID, "err", sErr)
		storyTitle, story = "", ""
	}
	hasStory := storyTitle != "" || story != ""

	// 可导出守卫（handler 已守卫，runner 再防一层）：≥1 分镜 且 (≥1 已采纳图 或 有成品文本)。
	// assetList 已由 ExportAssets 收口为 status='accepted'（HITL accept 门禁）；纯文本成品也合法。
	if len(shots) == 0 || (!picturebook.HasExportableImage(assetList) && !hasStory) {
		r.fail(ctx, job.ID, errors.New("作品尚未就绪（无已采纳资产或成品文本）"))
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

	// 标题：优先 LLM 成品文档 title；否则项目名；再否则 "export"。只在调用方加，
	// picturebook.Assemble 须保持与前端成品预览一致，不可把回落塞进 Assemble。
	title := storyTitle
	if title == "" {
		title = name
	}
	if title == "" {
		title = "export"
	}

	pages := picturebook.Assemble(title, shots, assetList)

	// Intro/cover 页（net-new，镜像 RunPreview 的 extractStoryDoc）：从 LLM 成品文档
	// 取 title+story，前插一页 cover。无成品文本时不前插（Assemble 已用项目名产 cover 页）。
	if hasStory {
		intro := picturebook.Page{Kind: "cover", Title: title, Narration: story}
		pages = append([]picturebook.Page{intro}, pages...)
	}

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

	data, contentType, err := render(title, pages, pageBytes)
	if err != nil {
		r.fail(ctx, job.ID, fmt.Errorf("render %s: %w", job.Format, err))
		return
	}

	bs, cfgID, _ := r.router.ResolveWriteTarget(ctx, orgID, projConfigID)
	if bs == nil {
		// ResolveWriteTarget 在配置坏掉时可能回落到 nil Default——别 panic，失败这单。
		r.fail(ctx, job.ID, errors.New("resolve write target returned nil blob store"))
		return
	}
	blobKey := fmt.Sprintf("exports/%s/%s%s", job.ProjectID, job.ID, extForFormat(job.Format))
	if err := bs.Put(ctx, blobKey, bytes.NewReader(data), contentType); err != nil {
		r.fail(ctx, job.ID, fmt.Errorf("put export artifact: %w", err))
		return
	}

	// 终态写用脱离取消的新 ctx：CallTimeout 触发或 shutdown 取消了 ctx 时，落库仍要成功，
	// 否则任务卡 running 直到被 Reap 用 "lease expired" 覆盖、丢失真实结果。
	doneCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := r.store.MarkDone(doneCtx, job.ID, blobKey, cfgID, int64(len(data))); err != nil {
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
	resp, err := assetHTTPClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("exports: http get %q: %w", key, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("exports: http get %q: status %d", key, resp.StatusCode)
	}
	// 读到 max+1 以便检出溢出：>max 即超限报错，绝不静默截断。
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAssetBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("exports: read body for %q: %w", key, err)
	}
	if int64(len(data)) > maxAssetBytes {
		return nil, "", fmt.Errorf("exports: asset %q exceeds max size %d bytes", key, maxAssetBytes)
	}
	return data, resp.Header.Get("Content-Type"), nil
}

// fail 包一层 MarkFailed，把 ErrNotRunning（竞态输家）当作良性。终态写脱离取消
// （context.WithoutCancel + 短超时），保证 CallTimeout/shutdown 取消 ctx 后失败原因仍落库。
func (r *Runner) fail(ctx context.Context, id string, cause error) {
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := r.store.MarkFailed(wctx, id, cause.Error(), r.maxAttempts, r.backoff); err != nil {
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
// 但 ExportAssets 额外 SELECT a.storage_config_id（artifacts.Assets 不返回此列）并按
// 宽松状态集（accepted/pending_acceptance/done）过滤——工作流运行不走 HITL accept。
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

// ExportAssets 取某 plan 的导出可用资产（含 storage_config_id，供读字节阶梯解析后端）。
// 仅取 status='accepted'：成品/成书是「已审核内容」的交付物，只能装订经审核台采纳的资产。
// （前端成品预览层为便于审核会显示 pending_acceptance 图，那是预览语义；导出交付物必须
// 严守 HITL accept 门禁——否则审核形同虚设：0 采纳也能导出整本。）
func (b *BookData) ExportAssets(ctx context.Context, projectID, planID string) ([]picturebook.Asset, error) {
	rows, err := b.db.WithContext(ctx).Raw(
		`SELECT a.id, a.shot_id, a.type, a.blob_key, a.status, a.version,
		        a.prompt, a.provider, a.model, a.storage_config_id
		 FROM assets a
		 JOIN todos t ON a.todo_id = t.id
		 WHERE a.project_id=$1 AND t.plan_id=$2
		   AND a.status = 'accepted'
		 ORDER BY a.created_at DESC`,
		projectID, planID).Rows()
	if err != nil {
		return nil, fmt.Errorf("exports: load export assets: %w", err)
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

// StoryDoc 从某 plan 的 custom LLM 节点产物解析出成品文档（title/story/lyrics）。
// 镜像前端 runPreviewModel.ts 的 extractStoryDoc：node_outputs.content 是一段 JSON
// 字符串（故事 {title,story,...}；音乐 {title,lyrics,...}）。取该 plan 下按 created_at
// 升序第一条能 JSON 解析且含 title/story/lyrics 的节点产物；无则返回空串（非错误）。
func (b *BookData) StoryDoc(ctx context.Context, projectID, planID string) (title, story, lyrics string, err error) {
	rows, err := b.db.WithContext(ctx).Raw(
		`SELECT n.content FROM node_outputs n
		 JOIN todos t ON n.todo_id = t.id
		 WHERE n.project_id=$1 AND t.plan_id=$2
		 ORDER BY n.created_at ASC`,
		projectID, planID).Rows()
	if err != nil {
		return "", "", "", fmt.Errorf("exports: load node outputs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var content string
		if err := rows.Scan(&content); err != nil {
			return "", "", "", fmt.Errorf("exports: scan node output: %w", err)
		}
		var doc struct {
			Title  string `json:"title"`
			Story  string `json:"story"`
			Lyrics string `json:"lyrics"`
		}
		if json.Unmarshal([]byte(content), &doc) != nil {
			continue
		}
		if doc.Title != "" || doc.Story != "" || doc.Lyrics != "" {
			return doc.Title, doc.Story, doc.Lyrics, rows.Err()
		}
	}
	return "", "", "", rows.Err()
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
