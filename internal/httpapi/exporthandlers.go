package httpapi

// exporthandlers.go 暴露工作流作品导出（PDF/EPUB/ZIP）的 HTTP 端点：创建导出任务、
// 列出某项目的导出历史、查询单个任务、下载产物。导出本身由 internal/exports 的异步 runner
// 消费 export_jobs 队列完成（T4），本文件只负责入队 + 鉴权 + 产物回源。
//
// 鉴权分两类：项目级三个端点走 projScope（按 {id} 项目的 org 解析角色）；产物下载
// /api/exports/{id}/content 走新增的 exportScope（按 job→project→org 解析角色），
// 因为它的路径里没有项目 id，只有 jobId。

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/costa92/llm-agent-studio/internal/exports"
	"github.com/costa92/llm-agent-studio/internal/picturebook"
	"github.com/costa92/llm-agent-studio/internal/project"
)

// ExportsStore 是导出任务队列的读写子集（由 *exports.Store 满足）。
type ExportsStore interface {
	Create(ctx context.Context, projectID, planID, format string) (exports.ExportJob, error)
	Get(ctx context.Context, id string) (exports.ExportJob, error)
	ListByProject(ctx context.Context, projectID string) ([]exports.ExportJob, error)
}

// ExportBookData 提供可导出判定所需的输入（分镜 + 导出可用资产 + LLM 成品文档，由
// *exports.BookData 满足）。
type ExportBookData interface {
	Shots(ctx context.Context, projectID, planID string) ([]picturebook.Shot, error)
	ExportAssets(ctx context.Context, projectID, planID string) ([]picturebook.Asset, error)
	StoryDoc(ctx context.Context, projectID, planID string) (title, story, lyrics string, err error)
}

// exportProjectReader 是创建/下载导出端点对项目库的需求子集（由 *project.Store 满足）。
type exportProjectReader interface {
	Get(ctx context.Context, id string) (project.Project, error)
	ListPlans(ctx context.Context, projectID string) ([]project.Plan, error)
	OrgIDForProject(ctx context.Context, projectID string) (string, error)
}

// validExportFormat 限定支持的导出格式。
func validExportFormat(f string) bool {
	switch f {
	case "pdf", "epub", "zip":
		return true
	default:
		return false
	}
}

// exportJobView 是导出任务对前端的投影（隐藏租约/attempts 等内部列）。
type exportJobView struct {
	ID        string `json:"id"`
	Format    string `json:"format"`
	Status    string `json:"status"`
	SizeBytes int64  `json:"sizeBytes"`
	Error     string `json:"error"`
	CreatedAt string `json:"createdAt"`
}

func toExportView(j exports.ExportJob) exportJobView {
	return exportJobView{
		ID:        j.ID,
		Format:    j.Format,
		Status:    j.Status,
		SizeBytes: j.SizeBytes,
		Error:     j.Error,
		CreatedAt: j.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

// createExportHandler (POST /api/projects/{id}/exports): roleEditor.
// 对任何有可导出运行（plan）的工作流项目开放（post-m23 项目一律 kind=custom，故不再按
// kind 门禁）；body {format, planId?}。planId 缺省时解析为最新 plan，单一解析出的 planId
// 落库（R2：runner 再按 job.PlanID 单 plan 收口）。未达可导出阈值 → 409。
func createExportHandler(projects exportProjectReader, store ExportsStore, book ExportBookData) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("id")
		var body struct {
			Format string `json:"format"`
			PlanID string `json:"planId"`
		}
		// 空 body（planId/format 全缺省）是合法请求：io.EOF 不算解码错误，format
		// 由下面的校验兜底。
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if !validExportFormat(body.Format) {
			http.Error(w, "unsupported format (want pdf|epub|zip)", http.StatusBadRequest)
			return
		}

		proj, err := projects.Get(r.Context(), projectID)
		if errors.Is(err, project.ErrNotFound) {
			http.Error(w, "project not found", http.StatusNotFound)
			return
		} else if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// 工作流门禁：post-m23 项目一律 kind=custom，导出对任何有 plan（运行）的项目开放；
		// 下面的 planId 解析在无 plan 时 409，即等价于「有运行才可导出」的门禁。
		_ = proj

		// 解析 planId：body 给定优先；缺省取最新 plan（ListPlans 按 created_at 降序，[0] 即最新）。
		planID := body.PlanID
		if planID == "" {
			plans, perr := projects.ListPlans(r.Context(), projectID)
			if perr != nil {
				http.Error(w, perr.Error(), http.StatusInternalServerError)
				return
			}
			if len(plans) == 0 {
				http.Error(w, "project has no plan to export", http.StatusConflict)
				return
			}
			planID = plans[0].ID
		}

		// 可导出守卫（宽松）：≥1 分镜 且 (≥1 导出可用图 或 有 LLM 成品文本)。不卡图数阈值——
		// 纯文本文档也合法。达不到就别入队（runner 会再防一层）。
		shots, err := book.Shots(r.Context(), projectID, planID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		assetList, err := book.ExportAssets(r.Context(), projectID, planID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		title, story, lyrics, err := book.StoryDoc(r.Context(), projectID, planID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		hasStory := title != "" || story != "" || lyrics != ""
		if len(shots) == 0 || (!picturebook.HasExportableImage(assetList) && !hasStory) {
			http.Error(w, "book not ready (no exportable image or story text)", http.StatusConflict)
			return
		}

		job, err := store.Create(r.Context(), projectID, planID, body.Format)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"jobId": job.ID})
	}
}

// listExportsHandler (GET /api/projects/{id}/exports): roleViewer. 该项目导出历史，最新在前。
func listExportsHandler(store ExportsStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jobs, err := store.ListByProject(r.Context(), r.PathValue("id"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]exportJobView, 0, len(jobs))
		for _, j := range jobs {
			out = append(out, toExportView(j))
		}
		writeJSON(w, http.StatusOK, map[string]any{"exports": out})
	}
}

// getExportHandler (GET /api/projects/{id}/exports/{jobId}): roleViewer.
// 校验 job.ProjectID == {id}：projScope 只校验 {id} 的 org，若不绑定到本项目，跨项目
// jobId 就能被同 org 的人窥探——这里收口为 404（不存在/不属于本项目）。
func getExportHandler(store ExportsStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("id")
		job, err := store.Get(r.Context(), r.PathValue("jobId"))
		if errors.Is(err, exports.ErrNotFound) {
			http.Error(w, "export job not found", http.StatusNotFound)
			return
		} else if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if job.ProjectID != projectID {
			http.Error(w, "export job not found", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, toExportView(job))
	}
}

// exportContentHandler (GET /api/exports/{id}/content): roleViewer（经 exportScope 鉴权）。
// 仅 done 任务可下载（未完成 → 404）；按 job 所属 org 路由对象存储读字节/重定向，
// 镜像 assetContentHandler 的回源阶梯。Content-Disposition 用项目名（回落 "export"）+ 扩展名。
func exportContentHandler(store ExportsStore, router BlobRouter, projects exportProjectReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		job, err := store.Get(r.Context(), r.PathValue("id"))
		if errors.Is(err, exports.ErrNotFound) {
			http.Error(w, "export job not found", http.StatusNotFound)
			return
		} else if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if job.Status != "done" {
			http.Error(w, "export not ready", http.StatusNotFound)
			return
		}

		orgID, err := projects.OrgIDForProject(r.Context(), job.ProjectID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		bs, err := router.BlobStoreForConfigID(r.Context(), orgID, job.StorageConfigID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// 文件名：项目名（回落 "export"）+ 格式扩展名。项目名是用户可控且常为中文/UTF-8，
		// 故走 RFC 6266/5987：ASCII filename 兜底 + filename*（UTF-8 百分号编码）。
		name := "export"
		if proj, perr := projects.Get(r.Context(), job.ProjectID); perr == nil && proj.Name != "" {
			name = proj.Name
		}
		w.Header().Set("Content-Disposition", exportContentDisposition(name, exportExt(job.Format)))

		// 回源阶梯：优先带 ctx 的直读直发，否则签名 302（镜像 assetContentHandler）。
		type ctxReader interface {
			ReadKey(ctx context.Context, key string) ([]byte, string, error)
		}
		if rdr, ok := bs.(ctxReader); ok {
			data, ct, err := rdr.ReadKey(r.Context(), job.BlobKey)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if ct != "" {
				w.Header().Set("Content-Type", ct)
			}
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Content-Security-Policy", "sandbox")
			_, _ = w.Write(data)
			return
		}
		signed, err := bs.SignedURL(r.Context(), job.BlobKey, signedURLTTL)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, signed, http.StatusFound)
	}
}

// exportContentDisposition 构造 attachment 的 Content-Disposition 头：同时给出经
// sanitize 的 ASCII filename 兜底 + RFC 5987 的 filename*（UTF-8 百分号编码），以兼容
// 含引号或中文/UTF-8 的项目名（RFC 6266）。name 已含回落（"绘本"），ext 形如 ".pdf"。
//   - ASCII 兜底：引号/反斜杠/控制字符/非 ASCII 一律替换为 '_'；sanitize 后只剩扩展名/
//     空（如纯中文名）时回落到 "export"+ext，避免下出空名文件。
//   - filename*：对完整 UTF-8 文件名按 RFC 5987 百分号编码（仅 unreserved 不编码）。
func exportContentDisposition(name, ext string) string {
	full := name + ext
	ascii := make([]byte, 0, len(full))
	for _, rn := range full {
		if rn < 0x20 || rn == 0x7f || rn > 0x7e || rn == '"' || rn == '\\' {
			ascii = append(ascii, '_')
			continue
		}
		ascii = append(ascii, byte(rn))
	}
	asciiName := string(ascii)
	if strings.Trim(strings.TrimSuffix(asciiName, ext), "_") == "" {
		asciiName = "export" + ext
	}
	return "attachment; filename=\"" + asciiName + "\"; filename*=UTF-8''" + rfc5987Encode(full)
}

// rfc5987Encode 百分号编码 s 的每个字节，仅保留 RFC 3986 的 unreserved 集合
// （A-Za-z0-9-._~），其余字节编码为大写 %HH（按字节、非按 rune，故对多字节 UTF-8 正确）。
func rfc5987Encode(s string) string {
	const upperhex = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(upperhex[c>>4])
		b.WriteByte(upperhex[c&0x0f])
	}
	return b.String()
}

// exportExt 把导出格式映射到下载文件扩展名。
func exportExt(format string) string {
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
