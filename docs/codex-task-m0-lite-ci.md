# Codex 任务：M0-lite——GitHub Actions CI + Makefile

主线转向监控系统产品（见 `codex-batches-plan.md` 顶部方向修正）。本批次是主线第一步：给仓库装上 CI 质量门，保护接下来 M1/M2 的全部批次开发。**刻意瘦身**：不做发布打包、不做 Agent 重构，半天量。

## 背景速读

- 仓库根目录即 Go module（`controltower`，go 1.24），无任何 CI；测试 `go test ./...` 当前 23 个包全绿。
- Agent 入口 `agent/cmd/control-tower-agent`，Server 入口 `server/cmd/control-tower-server`；Web 是纯静态文件无构建步骤。
- `.gitattributes` 已强制 `*.sh` 等 LF；构建产物目录 `dist/` 已在 `.gitignore`。

## 硬性纪律

1. 零新依赖（不引入任何 Go module；Actions 只用官方 `actions/checkout`、`actions/setup-go`）。
2. 不改任何现有 Go/Web 源码——本批次只新增 `Makefile`、`.github/workflows/ci.yml`、文档更新。
3. 所有新文件 UTF-8 无 BOM、LF。

## 工作项

### 任务 1：Makefile（仓库根目录）

目标（全部 `.PHONY`）：

- `make test`：`go vet ./...` && `go test ./...`
- `make build-agent`：交叉编译 `CGO_ENABLED=0` linux/amd64 与 linux/arm64 到 `dist/control-tower-agent-linux-{amd64,arm64}`，`-trimpath -ldflags "-s -w"`
- `make build-server`：同参数编译 server 到 `dist/control-tower-server-linux-amd64`
- `make build`：依赖上两者

版本注入本批次**不做**（agentVersion 仍为 const，留给将来发布批次），Makefile 里留一行注释说明。

### 任务 2：CI 工作流 `.github/workflows/ci.yml`

- 触发：`push` 到 `main` 与所有 `pull_request`。
- 单 job（ubuntu-latest）：checkout → setup-go（go-version `1.24.x`，开启内置缓存）→ `make test` → `make build`。
- 构建产物不上传（无 artifacts 步骤），CI 只做质量门。
- 并发控制：`concurrency: { group: ci-${{ github.ref }}, cancel-in-progress: true }`，避免连续 push 排队。

### 任务 3：文档

- 根 `README.md`：追加简短的 Development 一节（make test / make build 两行说明 + CI 徽章链接 `https://github.com/yanceylv518/controlTower/actions/workflows/ci.yml/badge.svg`）。
- `docs/development-progress.md`：M0 相关表格标记"CI + Makefile 已完成（M0-lite，发布打包与 Agent 重构挂起）"。

## 完成标准

1. 本地 `make test`、`make build` 可执行且通过（无 make 环境则在交付说明注明用等价命令验证过）。
2. push 后 GitHub Actions 运行绿色（提交后在 PR/commit 页面确认）。
3. 一个 commit：`ci: add github actions quality gate and makefile (M0-lite)`。

## 明确不做

- 发布打包（tar.gz/SHA256/Release）、版本注入、Agent 渠道快照常驻化——全部留给 v2.0 发布前的 Agent 升级批次。
- 不动 `agent/**`、`server/**`、`web/**` 源码。
