# Codex 批次执行计划（v1.1 → M0 → M1）

本文档把双轨路径（`development-plan.md`）的近期工作切成 **Codex 可独立执行的批次**。执行规则：

- **一批次一个 codex-task 文件**，上一批次 review 通过后才生成下一批次的任务文件（避免返工连锁）。
- 每批次固定四段：开发思路（为什么这么做）→ 改动范围 → 验收（Claude review）→ **人工验证点（用户做什么、预期看到什么、耗时）**。
- 通用纪律沿用 `codex-task-web-monitoring-fixes.md` 的硬性纪律段（编码转义、零新依赖、LF、fail-safe、测试配套）。
- 涉及 `agent/**` 的批次必须保持独立告警模式向后兼容：不配新参数时行为与当前生产版本一致。

## 批次总览与人工验证节奏

| 批次 | 内容 | 依赖 | 人工验证点（何时/多久） |
| --- | --- | --- | --- |
| **B1** | 慢返回窗口规则 + episode 事件持久化 | 无 | review 通过后升级测试/生产 Agent，~15 分钟 |
| **B2** | 证据驱动主动探测（含立即复核） | B1（共用事件日志与发送通道） | 提供 admin 凭证后开启探测，人为弄坏一个渠道 key，~30 分钟 |
| **B3** | 静默/吞吐骤降→探测确认 + 正向证据恢复 + episode 三收尾 | B2（依赖探测确认） | 模拟挂起 + 禁用/启用渠道走一遍收尾流程，~30 分钟 |
| **B4** | M0：Makefile + CI + 发布打包 + 渠道快照常驻化 | 无（可与 B2/B3 并行） | 打 tag → 下载 CI 产物 → 测试机安装升级，~30 分钟 |
| **发布点** | **v1.1 上线**：用 B4 的发布包把 B1~B3 全部功能升级到生产 | B1~B4 | **生产观察一周**：钉钉消息质量、误报率、探测成本 |
| M1-B1 | Server 认证体系（users/sessions/登录锁定） | v1.1 上线后启动 | 浏览器手测登录/登出/改密/锁定，~20 分钟 |
| M1-B2 | 实例管理 + 按实例 Agent Token + 多实例过滤 | M1-B1 | 建实例、轮换 token、双实例上报隔离，~30 分钟 |
| M1-B3 | 告警事件时间线 + 通知强化（重试上限/手动重发/钉钉加签） | M1-B1 | Web 上走告警生命周期 + 重发死信，~20 分钟 |
| M1-B4 | 渠道命令闭环 + API 硬化 + 契约冻结 | M1-B2 | Web 停用/调权一个测试渠道并核对审计，~20 分钟 |

M1 各批次的详细任务文件在到达时生成（届时按当时代码现状写，避免过期指令）。以下给出 B1~B4 的开发思路，B1 的完整指令见 `codex-task-v1.1-b1.md`。

---

## B1：慢返回窗口规则 + episode 事件持久化

**开发思路**：
1. 慢返回与错误窗口**同构复用**——`erroralert` 的 `outcome` 已带时间戳，增加 `isSlow` 标记；每个维度的窗口切片保留 `max(错误窗口, 慢窗口)` 条，计数时各取各的最近 N 条。不新建包、不复制 episode 机制。
2. 错误和慢返回是**两条独立 episode**（各自的 alerted/提醒/重臂状态）：渠道可能"不报错但全变慢"，两者混在一个状态里会互相掩盖。实现上把 `alerted/episodeStartAt/episodeErrors/lastRemindAt` 抽成 `ruleState` 结构，每维度持有 error/slow 两份。
3. 流式误报防护：流式长回答天然慢，`CT_ALERT_SLOW_STREAM_SECONDS`（默认 300）给流式单独阈值，设 0 则流式完全不参与慢判定。
4. episode 事件持久化按 v1.1 设计第 8 节：所有 episode 状态变迁（alert/remind/rearm，含 rule 标记）追加写 `CT_DATA_DIR/alert-events.jsonl`，5MB 轮转保留 1 个旧文件，写失败只记一次日志绝不影响告警——这是渠道 26 事故"调查靠翻钉钉群"教训的直接产物，也是将来 Web 可见性的地基。

**人工验证点**（review 通过、升级 Agent 后，~15 分钟）：
1. `journalctl -u control-tower-agent -f` 看到每轮审计日志正常。
2. 临时把 `CT_ALERT_SLOW_SECONDS=1` 重启——真实流量下几分钟内必有慢返回告警进钉钉群，消息标题为"渠道/客户慢返回激增"；验完改回 120 重启。
3. `cat /var/lib/control-tower-agent/alert-events.jsonl` 能看到刚才的 alert 事件记录。

## B2：证据驱动主动探测

**开发思路**：
1. 新包 `agent/internal/prober`，独立 goroutine 调度（不挤在采集 pass 里），每 15 秒扫描一次到期渠道，按渠道 ID 散列错峰。
2. 渠道清单直接查 `channels` 表（只读，复用 log DSN 连接，只取启用状态），10 分钟缓存——不依赖 admin API 拿列表，admin 凭证只用于发探测。
3. 探测动作复用 `channelcontrol` 的登录/token 逻辑，新增 `TestChannel(ctx, channelID, model)` 方法调 new-api 自带的渠道测试接口，15 秒超时。
4. **证据驱动省钱**：主循环每轮把采集到的完成事件喂给 prober（`ObserveCompletions`），记录每渠道最近成功时间；探测时点若 2 分钟内有成功完成则跳过；无流量渠道用 300 秒放宽间隔。
5. **失败立即复核**：失败后 15 秒补测一次，连续 2 次失败告警（发送复用 `Notifier` 暴露的原始发送方法），episode/提醒语义与 B1 一致，事件进 jsonl（kind=probe_fail）。
6. 全程 fail-safe：admin 登录失败、channels 无权限时探测自动停摆并每小时日志提示一次，不影响其余告警。

**人工验证点**（需先在配置里补 admin 凭证并 `CT_PROBE_ENABLED=true`，~30 分钟）：
1. 重启后 journal 看到探测调度日志；确认忙渠道被跳过（省钱生效）。
2. 在 new-api 后台把一个测试渠道的上游 key 改错 → **3 分钟内**钉钉收到"渠道探测失败"告警。
3. 改回 key → journal/jsonl 看到探测恢复成功计数（恢复消息在 B3）。
4. 次日核对该渠道上游用量，确认探测成本符合预期（几厘钱级）。

## B3：静默/吞吐骤降确认 + 正向证据恢复 + episode 三收尾

**开发思路**：
1. 静默检测挂在采集 pass 上（数据就在手里）：每渠道记 15 分钟完成数（基线）与最近完成时间；两档线索——零完成 3 分钟（全静默）、完成速率跌破基线 50% 持续 3 分钟（骤降）——**线索只触发 prober.ProbeNow()（骤降触发 3 连发），结论由实测给**；未开探测只发"疑似"预警级。
2. 恢复通知按设计第 D 节的**正向证据表**实现：错误/慢返回恢复 = 告警后新完成 ≥10 且坏 ≤1；探测恢复 = 连续 3 次成功；静默恢复 = ≥10 条新**成功**（504 洪峰转错误窗口不发恢复）。防抖：满足后多观察一个周期、每 episode 一条。
3. episode 三收尾：渠道状态轮询（channels 表 status 字段，与名字缓存同一次查询）——被禁用 → 【已处置】立即闭环；重新启用 → ProbeNow 验证，通过发【重新上线】、失败重新告警；`CT_EPISODE_MAX_HOURS=24` 超时兜底。
4. 消息全部只陈述事实（"告警后新完成 12 条，0 失败，探测 3 次通过，故障持续约 18 分钟"）。

**人工验证点**（~30 分钟）：
1. 测试渠道 key 弄坏让其触发告警 → 修好 → 观察【恢复】消息带证据数字。
2. 告警中的渠道在 new-api 后台点禁用 → 收到【已处置】；重新启用 → 收到【重新上线】（或探测失败重新告警）。
3. 有条件的话 iptables 对上游丢包模拟挂起，计时静默确认告警是否在 ~3.5 分钟内到达。

## B4（M0）：Makefile + CI + 发布打包 + 渠道快照常驻化

**开发思路**：
1. Makefile：`test`（vet+test）、`build-agent`（amd64/arm64，`-ldflags -X main.agentVersion=$(VERSION)`，agentVersion 从 const 改 var）、`package`（tar.gz：二进制 + install-agent.sh + service + config example + README + SHA256SUMS，打包前强制 `sed CRLF→LF` 双保险）。
2. GitHub Actions 两条工作流：push/PR 跑 vet+test；打 `v*` tag 跑构建打包并创建 Release 挂产物——**从此发布产物永远出自 Linux CI**，Windows 手工打包事故类（CRLF/编码/漏重启）从流程上根除。
3. 渠道快照常驻化（遗留 P0，只影响上报模式）：`run()` 启动期创建常驻 DB 连接与 channelcollector 实例，采集 pass 只执行查询；独立告警模式行为不变。
4. 部署包用 tar.gz（顺带解决 Ubuntu 无 unzip 的踩坑）。

**人工验证点**（~30 分钟）：
1. 推一个测试 tag（如 v1.1.0-rc1）→ GitHub Actions 全绿 → Release 页面有 amd64/arm64 两个 tar.gz + 校验和。
2. 下载 tar.gz 到测试机（或直接生产）：`tar xzf` → `sudo ./install-agent.sh --config ...` 一次成功，无需任何 sed 修换行。
3. `journalctl` 确认新进程版本号为 tag 版本（版本注入生效）。

## v1.1 发布点（B1~B4 之后）

用 B4 的流水线打正式 tag `v1.1.0`，生产升级，**观察一周**：钉钉消息质量与频率、慢返回/静默/探测的误报漏报、探测成本。观察结论写入 `iteration-log.md` v1.1 章节的"已知限制/遗留问题"，并作为是否启动 v1.2（在途检测中间件）的数据依据。之后进入 M1（批次任务文件届时生成）。
