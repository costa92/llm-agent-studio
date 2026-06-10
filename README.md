# llm-agent-studio

AI Studio — Multi-Agent 内容生产平台（生态 sibling 案例项目）。

## 开发

独立 sibling 仓，所有 Go 命令需 `GOWORK=off`：

```bash
GOWORK=off go test ./...
```

提交时 replace-guard 钩子会自动剥离本地 `replace`（如已安装生态钩子）。

## 里程碑

- **M1 (v0.1.0)**：文本管线骨架 —— project + LLM Planner（动态规划 + 校验/回落）+ todo 图 worker（租约队列）+ Script/Storyboard agent + SSE 时间线 + authz/RBAC + otel。
- **M2 (v0.2.0)**：图片生成 + 人工审核 + 资产库 —— BlobStore（localfs HMAC 签名 / S3 minio）+ MediaGenerator（image 适配 contract/llm.ImageGenerator）+ PromptBuilder 风格库 + AssetAgent + 按 shot 扇出 + HITL 采纳/退回/重生成（admin）+ 资产版本血缘 + 资产库检索 + 用量账本 + model_configs。

### M2 已知限制（M3 处理）

- **模型路由未接线**：`model_configs` CRUD + `DefaultForOrg` + `Registry.Resolve` 已落地并可配置，但 `runAsset` 当前绑定单一默认 generator（`PROVIDER`/`MODEL` 环境变量）；按 org/模型路由留待 M3。管理员配置的默认模型 M2 暂不生效。
- **成本未计价**：`generations` 账本记录每次调用（provider/model/tokens/张数/耗时），但 `cost_micros` 恒为 0；真实计价表留待 M3。成本中心金额 M2 仅为占位。
- **取消语义**：取消项目只取消 todo，不取消正在生成的资产；扇出/重生成的崩溃窗口幂等防护到位，但取消中途的在途资产清理留待 M3。
