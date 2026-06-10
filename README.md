# llm-agent-studio

AI Studio — Multi-Agent 内容生产平台（生态 sibling 案例项目）。

## 开发

独立 sibling 仓，所有 Go 命令需 `GOWORK=off`：

```bash
GOWORK=off go test ./...
```

提交时 replace-guard 钩子会自动剥离本地 `replace`（如已安装生态钩子）。
