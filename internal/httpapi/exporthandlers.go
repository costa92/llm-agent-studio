package httpapi

// exporthandlers.go 暴露绘本成书导出（picturebook 成书导出）的 HTTP 端点：创建导出任务、
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

// ExportBookData 提供成书阈值判定所需的输入（分镜 + 已审核资产，由 *exports.BookData 满足）。
type ExportBookData interface {
	Shots(ctx context.Context, projectID, planID string) ([]picturebook.Shot, error)
	AcceptedAssets(ctx context.Context, projectID, planID string) ([]picturebook.Asset, error)
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
// 仅对 picturebook 项目开放；body {format, planId?}。planId 缺省时解析为最新 plan，
// 单一解析出的 planId 落库（R2：runner 再按 job.PlanID 单 plan 收口）。未达成书阈值 → 409。
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
		if proj.Kind != "picturebook" {
			http.Error(w, "export is only available for picturebook projects", http.StatusBadRequest)
			return
		}

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

		// 成书阈值守卫：达不到阈值就别入队（runner 会再防一层）。
		shots, err := book.Shots(r.Context(), projectID, planID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		assetList, err := book.AcceptedAssets(r.Context(), projectID, planID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !picturebook.IsBookReady(shots, assetList) {
			http.Error(w, "book not ready (insufficient accepted images)", http.StatusConflict)
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
// 镜像 assetContentHandler 的回源阶梯。Content-Disposition 用项目名（回落 "绘本"）+ 扩展名。
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

		// 文件名：项目名（回落 "绘本"）+ 格式扩展名。
		name := "绘本"
		if proj, perr := projects.Get(r.Context(), job.ProjectID); perr == nil && proj.Name != "" {
			name = proj.Name
		}
		filename := name + exportExt(job.Format)
		w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")

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
