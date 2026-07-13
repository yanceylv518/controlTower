# Codex 任务：v1.1-B5——Nginx timing 日志告警（信号 E）

Agent 新增可选模块：tail 生产 Nginx 的 `timed` 访问日志，做 504 即时告警、TTFT（`uht`）告警，告警消息带分段归因。设计依据：`design-v1.1-early-warning.md` 信号 E 与 `latency-diagnosis.md`。

**文末自查清单粘贴进 commit message。**

## 背景速读

- 生产两台 new-api 前的 Nginx 已启用 `timed` 日志格式（字段见下），Agent 与 Nginx 同机，可只读访问日志文件。
- 现有告警链路：`agent/internal/erroralert`（窗口规则 + episode 去重 + 提醒 + `eventlog.go` 写 `alert-events.jsonl`），钉钉发送在 `erroralert.go` 的私有 `send()`（含 errcode 检查与"告警"关键字前缀）。
- 独立模式与完整模式都要支持本模块；模块与 logs 采集互不依赖。
- 日志样例（字段名固定，顺序固定）：

```
1.2.3.4 - "POST /v1/chat/completions HTTP/1.1" [13/Jul/2026:10:00:00 +0800] status=200 rt=12.345 uct=0.001 uht=1.234 urt=12.344 bytes=45678 req_len=890
```

- 字段坑：`uct`/`uht`/`urt` 可能为 `-`（无 upstream，如 4xx 直接返回）；Nginx 层自身重试时为逗号分隔多值（解析时**求和**）；`rt` 恒为单值。

## 硬性纪律

- **失效安全是本批次第一验收项**：`CT_NGINX_ACCESS_LOG` 留空 → 模块完全不启动，零日志零副作用；配置了但文件不存在/无读权限 → 启动时 WARN 一条，之后每 30 秒静默重试打开，**绝不 panic、不退出进程、不影响其他告警信号**；单行解析失败只跳过（内部计数），不告警不刷屏。
- 零新增外部依赖；只用标准库。
- 现有业务代码唯一允许的改动：把 `erroralert.go` 中的钉钉发送逻辑提取为 `agent/internal/dingtalk` 包（`Send(ctx, webhookURL, content) error`，行为逐字节不变，含 errcode 检查与 30 秒超时），`erroralert` 改为调用它——现有测试必须原样通过。
- 所有脚本/文件 LF；不改 Nginx 配置、不写任何 Nginx 相关文件（只读）。

## 工作项

### 任务 1：解析器与 tailer（新包 `agent/internal/nginxtiming`）

- `ParseLine(line string) (Entry, bool)`：提取 `status`、`rt`、`uct`、`uht`、`urt`、`bytes`、`$request` 里的 method+path（只留 path 首段用于消息展示，**不落盘完整 URL**，防 query 里带敏感信息）。缺 `status=` 或 `rt=` 视为非 timed 行，返回 false。`-` 视为 0/缺失；逗号多值求和。
- Tailer：启动时 **seek 到文件末尾**（不回放历史，避免重启告警风暴）；逐行读取；轮转检测（inode 变化或文件变小→重新打开从头读）；文件消失→按失效安全纪律重试。
- 内部统计：已解析行数、跳过行数、当前是否在跟踪文件——每 10 分钟打一行 info 日志（如 `nginx timing: 1234 parsed, 5 skipped`），便于确认模块活着。

### 任务 2：告警规则（同包，独立于 erroralert 的状态机，语义对齐）

维度只有一个（实例级，Nginx 日志辨不出渠道/客户）。三条规则，episode 去重 + 恢复重臂 + 提醒间隔语义与 erroralert 一致（提醒间隔复用 `CT_ALERT_REMIND_MINUTES`）：

1. **504 即时**：出现 `status=504` 立即告警（首条即发，episode 内不重复，仅按提醒间隔重发并带累计数）；消息含 rt/uht/urt 与 path。
2. **5xx 窗口**：最近 `CT_NGINX_5XX_WINDOW`（默认 20）条中 `status>=500`（不含已单独告警的 504）达到 `CT_NGINX_5XX_THRESHOLD`（默认 5）→ 告警。
3. **TTFT 窗口**：最近 20 条**有 upstream 的**请求中，`uht >= CT_NGINX_TTFT_SECONDS`（默认 10）的达到 5 条 → 告警。消息必须带**分段归因**，格式：

```
[告警] Nginx TTFT 升高（实例 <instance>）
最近 20 条中 6 条首字节 ≥10s（中位 14.2s，最大 31.0s）
归因：首字节段慢（new-api/上游前段）；传输段正常（urt−uht 中位 0.8s）
```

若同窗口 `urt−uht` 也普遍偏大，归因行改为"传输段亦慢（流式/链路），见 latency-diagnosis.md"。

- 窗口带时间衰减，复用 `CT_ALERT_WINDOW_MAX_AGE_MINUTES` 语义。
- 告警事件写入既有 `alert-events.jsonl`（复用 `eventlog` 写入器；rule 取 `nginx_504` / `nginx_5xx` / `nginx_ttft`）。
- episode 状态仅存内存，重启即重臂（可接受，注释注明）。

### 任务 3：配置与接线

- `config.Config` 新增：`NginxAccessLog`（`CT_NGINX_ACCESS_LOG`，默认空=禁用）、`NginxTTFTSeconds`（默认 10）、`Nginx5xxWindow`（默认 20）、`Nginx5xxThreshold`（默认 5）。
- `main.go`：`CT_NGINX_ACCESS_LOG` 非空且 `CT_DINGTALK_WEBHOOK_URL` 非空时启动一个常驻 goroutine（独立于 pass 循环，随进程 ctx 退出）；独立模式与完整模式一致。webhook 为空而日志路径非空 → 启动时 WARN"nginx timing 已配置但无钉钉 webhook，模块不启动"。

### 任务 4：文档与示例

- `deploy/agent.config.example`、`deploy/agent.standalone.config.example`、`deploy/agent.env.example`：追加 4 个新变量并注释（含"留空禁用、缺文件不报错"说明）。
- `agent/README.md`：新增"Nginx timing 日志告警"小节（启用前提：Nginx 已配 `timed` 格式，链接 `docs/latency-diagnosis.md`；Agent 运行账号需对日志有读权限，注明 `setfacl -m u:ct-agent:r` 或加 adm 组两种做法）。
- `docs/development-progress.md` 记一行。

## 验证要求

1. `make test` 全绿；新增测试覆盖：
   - 解析：正常行、`-` 值、逗号多值求和、非 timed 行返回 false、query 不入 Entry；
   - tailer：临时文件追加行被读到；轮转（rename+新建）后继续读；**文件不存在时构造 tailer 不报错且持续重试**（缩短重试间隔的测试钩子）；
   - 规则：504 即时+episode 去重；5xx 窗口阈值；TTFT 窗口与归因文案分支（传输段正常/亦慢两种）；
   - 失效安全：日志路径为空时模块零启动（main 层用配置单测覆盖）。
2. 手工冒烟（本机）：临时文件当日志，`printf` 追加一条 504 行与 6 条高 uht 行，用假 webhook（本地 httptest 不可行则打日志验证内容），把过程记入交付说明。

## 交付前自查清单（填好粘贴进 commit message）

- [ ] 失效安全四场景逐一自测：未配置零启动 / 文件缺失只 WARN 且重试 / 无权限同上 / 脏行静默跳过
- [ ] dingtalk 包提取后 erroralert 现有测试原样通过，发送行为逐字节不变
- [ ] 三条规则的告警文案含分段归因；事件写入 alert-events.jsonl
- [ ] tailer 首次启动 seek 到末尾（不回放历史）；轮转检测有测试
- [ ] 4 个配置示例文件与 agent/README 已更新
- [ ] 一个 commit：`feat(agent): nginx timing log alerts with segment attribution (v1.1-B5)`

## 明确不做

网关开销分解探测（无 key 握手基线，归挂起的 v1.1 探测批次）；渠道/客户维度归因（Nginx 日志无此信息）；nginx 指标上报 Server / 入库（后续再议）；修改 Nginx 配置或 logrotate；钉钉加签（沿用关键字模式）；episode 状态持久化。
