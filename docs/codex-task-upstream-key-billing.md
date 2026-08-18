# Codex 任务：上游渠道 key 账单（多渠道按 key 指纹合并视图）

背景与定稿：design-upstream-key-billing.md（2026-08-18 用户拍板三点：指纹=SHA2(base_url+key)、显示名=base_url+key 尾4位、落映射快照表）。数据地基=billing_channel_daily_versions 渠道日行已存,**本批无新聚合、不重生成账单、不碰账单生成路径**。

**文末自查清单粘贴进 commit message；禁止 force push；Linux 跑全量 `go test ./...` + `pnpm -r typecheck` + `pnpm -r build`。**

**红线（安全,违者整批打回）**：渠道 key 明文不得离开 newapi 数据库——所有指纹/尾4位必须在 newapi SQL 内计算（SHA2/RIGHT）,SELECT 投影禁止出现裸 `key` 列;CT 侧任何表/结构体/日志/响应不得含完整 key。契约测试必须钉：映射查询 SQL 中 `key` 仅出现在 `SHA2(` 与 `RIGHT(` 内部。

## 1. 迁移 045（单条增量 ALTER/CREATE,注释禁分号）

`billing_upstream_channels` 映射快照表：instance_id VARCHAR(64), channel_id BIGINT, upstream_fp CHAR(64), base_url VARCHAR(255) DEFAULT '', key_tail VARCHAR(8) DEFAULT '', channel_name VARCHAR(128) DEFAULT '', updated_at DATETIME(6), PRIMARY KEY(instance_id,channel_id), KEY idx_upstream_fp(instance_id,upstream_fp)。

## 2. server：映射刷新（passthrough 直连）

PassthroughHandler 新方法：对站点只读库执行

```sql
SELECT id, COALESCE(name,''), COALESCE(base_url,''),
       SHA2(CONCAT(COALESCE(base_url,''),'|',`key`),256),
       RIGHT(`key`,4)
FROM channels
```

→ upsert 进 billing_upstream_channels（整批 REPLACE 语义:已删渠道的旧行**保留**——这正是快照表的目的）。刷新时机：上游账单列表接口被调用时顺带刷新（失败降级用已有快照,不 500;日志记失败）。multi-key 渠道整包哈希/整包尾4位,不拆（代码注释记档）。

## 3. server：读接口（复用渠道账单读闸 billingJobForRead 语义,409 一致）

- `GET /api/dashboard/billing/upstream-channels?instance_id&from&to[&job_id]`：分组列表——快照表按 upstream_fp 分组 join QueryBillingChannelAggregates（成员 channel_id 集合）,每组返回 upstream_fp、display_name(base_url+" …"+key_tail)、成员渠道数/清单(id+name)、区间合计（request_count/prompt/completion/cache/cache_write tokens/quota）;**有账单行但快照表无映射的渠道归"未归组"桶**（渠道删于快照表建立前）,单列一组 upstream_fp='';
- `GET /api/dashboard/billing/upstream-channels/detail?instance_id&fp&from&to[&job_id]`：组详情——日×模型明细行（成员合并,AggregateRow 口径）+成员渠道小计对照（每渠道一行区间合计）;
- CSV 导出：组详情两段（日×模型明细+成员小计）,BOM、命名沿用 billingDownloadName 惯例。xlsx 不做（一期）。
- admin 专用（viewer 本就被 auth 白名单挡在 billing 外,无需额外闸,但 handler 内仍按渠道账单页现行角色校验惯例对齐）。

## 4. 前端：渠道账单页新增"上游渠道"视角（布局用户已定:树形合计表）

- 渠道账单页（ChannelBillingV4）加切换器："按渠道 | 按上游 key"；
- **树形表格,默认全部展开**：组行=合计（显示名 base_url+" …"+key_tail/成员数/请求数/prompt/completion/cache tokens/quota）,子行=成员渠道（模型名+渠道名+#id+该渠道区间小计,与组行同列对齐）——**key↔渠道对应关系常驻可见,不藏在点击后**;一模型一渠道故成员行天然=模型分解,不另造模型层;
- 列序按对账主次：请求数/tokens 前置,quota 靠右标"参考";
- 组行操作：[明细] 抽屉=该组按日×模型明细表（对上游按天核对用）+抽屉内 CSV;[CSV]=直接导出（日明细+成员小计两段）;
- 搜索框同时匹配 渠道名/模型名/base_url（搜成员浮整组）;
- 未归组桶（快照缺失）默认折叠在末尾,展开列 channel_id。

## 5. 测试要求

1. 契约：映射 SQL 的 key 红线断言（见上）;045 单语句迁移契约;
2. 快照 upsert：新渠道插入/改 key 后指纹更新/**newapi 已删渠道旧行保留**;
3. 分组合并：多渠道同 fp 合计正确（含跨渠道同模型行相加）、未归组桶、job 绑定 409 路径;
4. handler 测试用 stub source（照 billing_requests 旧例的 stub 模式）;
5. CSV 两段结构断言。

## 6. 明确不做（记档）

- 手工分组覆盖/改名;跨站点合并;上游侧价格配置与金额对账（金额=quota 参考）;组内请求级明细;multi-key 拆单 key;xlsx 工作簿（CSV 先行）。

## 自查清单（粘贴进 commit message）

- [ ] go test ./... 全绿（Linux）
- [ ] pnpm -r typecheck && pnpm -r build 全绿
- [ ] key 红线契约测试在（SQL 中 key 仅现于 SHA2/RIGHT 内）
- [ ] 045 迁移单语句,契约测试在
- [ ] 快照保留已删渠道测试在;未归组桶测试在
- [ ] 读闸 409 语义与渠道账单页一致
