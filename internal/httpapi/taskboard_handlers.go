package httpapi

import (
	"context"
	"net/http"

	"github.com/costa92/llm-agent-studio/internal/studiosvc"
)

// TaskBoardReader 读任务中心聚合（satisfied by *studiosvc.TaskBoard）。
type TaskBoardReader interface {
	Board(ctx context.Context, orgID string) ([]studiosvc.TaskRow, error)
}

// taskboardHandler (GET /api/orgs/{org}/tasks): viewer+。返回每项目运行快照 +
// 内存计算的分桶计数。状态分桶镜像前端：planning/running→running, review→review,
// failed→failed, completed→completed, draft→draft, canceled→仅计入 all；每行计 all。
func taskboardHandler(r TaskBoardReader) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		items, err := r.Board(req.Context(), req.PathValue("org"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if items == nil {
			items = []studiosvc.TaskRow{}
		}
		counts := map[string]int{
			"all": 0, "running": 0, "review": 0, "failed": 0, "completed": 0, "draft": 0,
		}
		for _, it := range items {
			counts["all"]++
			switch it.Status {
			case "planning", "running":
				counts["running"]++
			case "review":
				counts["review"]++
			case "failed":
				counts["failed"]++
			case "completed":
				counts["completed"]++
			case "draft":
				counts["draft"]++
				// canceled: counted toward all only.
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items, "counts": counts})
	}
}
