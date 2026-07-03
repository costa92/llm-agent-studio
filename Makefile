# llm-agent-studio 构建/测试入口。CI（.github/workflows/ci.yml）与本地共用同一批目标。
#
# 约束：
# - Go 命令一律 GOWORK=off：上层 umbrella 有 go.work，不关掉会用 workspace 掩盖 go.mod 的真实依赖图。
# - DB 测试必须 -p 1 + fresh database：并行迁移有 race，脏库会撞 transient 唯一索引。

GO := GOWORK=off go
PNPM := pnpm --dir web

# 额外 go test 参数。CI 传 GOTESTFLAGS=-v 让 skip 消息可见，用于防假绿断言
# （grep 输出中的 "set LLM_AGENT_STUDIO_PG_URL"，出现即说明 DB 测试被 skip）。
GOTESTFLAGS ?=

.PHONY: build vet test test-db web-install web-lint web-test web-build ci

build:
	$(GO) build ./...

vet:
	$(GO) vet ./...

## test: 无 PG 的快速单测（DB-gated 测试自动 skip）。
test:
	$(GO) test -count=1 $(GOTESTFLAGS) ./...

## test-db: 全量测试，要求 LLM_AGENT_STUDIO_PG_URL 指向一个 fresh database。
test-db:
ifndef LLM_AGENT_STUDIO_PG_URL
	$(error LLM_AGENT_STUDIO_PG_URL 未设置：请指向一个全新的 PostgreSQL 数据库，例如 postgres://user:pw@host:5432/studio_test?sslmode=disable)
endif
	$(GO) test -p 1 -count=1 $(GOTESTFLAGS) ./...

web-install:
	$(PNPM) install --frozen-lockfile

web-lint:
	$(PNPM) lint

web-test:
	$(PNPM) test

web-build:
	$(PNPM) build

## ci: CI 全量入口（web-lint 有预存红，见 ci.yml，不进 ci 目标）。
ci: vet build test-db web-install web-test web-build
