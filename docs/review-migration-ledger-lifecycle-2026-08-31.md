# 验收记录：迁移台账 + 服务生命周期加固（2026-08-31）

- 范围：`4cf58ca fix: harden migrations and service lifecycles`
- 结论：**通过**。

## 根因缺陷（本笔修复的核心）

迁移历来每次启动全量重放。052 内含
`DELETE FROM billing_user_daily_active`——statements 时代（rc68+）compact
账单不再写 billing_request_details,该 DELETE 在**每次 server 重启**时清掉
compact 账单的生效指针且无法从旧明细重建,新账单在重启后不可见。
（生产尚未部署 statements 系列,此缺陷未在生产暴露;冒烟/E2E 环境实存。）

## 修复三件套

1. **schema_migrations 台账**:专用连接 GET_LOCK(1800s,advisory 锁按连接
   域,防双副本并发)+按文件名记账,已应用即跳过。采用语义=既有安装最后
   全量重放一次（与历来每次启动重放等价）,此后启动零重放。
2. **051/052 去毒化**（适配最后一次采用重放）:DELETE 移除、
   INSERT→INSERT IGNORE（绝不覆盖 compact 指针）、day_status 修复加
   WHERE EXISTS 限定 legacy 台账日;契约测试钉住禁 DELETE/须 IGNORE。
3. **062 修复历史损伤**:从 billing_compact_daily_totals × 完成的 generate
   任务恢复被 052 清掉的指针,INSERT IGNORE 不覆盖现存生效指针,窗口函数
   取最新任务;文件序保证 062 在 051/052 之后执行。

## 生命周期加固（server main）

- 信号上下文（SIGINT/SIGTERM）→ HTTP Server.Shutdown(30s,超时强制 Close,
  二次信号可强杀)→ workerGroup 统一取消 + 30s 有界等待。
- 全部后台循环（清理/聚合/保留/通知/调权/账单 runner/回滚 runner）纳入
  workerGroup,ticker 循环全部 select ctx.Done;worker 组两条单测钉住。
- agent 仅新增测试（渠道快照节流的状态复用),无生产代码改动。

## 新工作规则（此后必须遵守）

**台账采用后,修改既有迁移文件对已部署安装永不生效（按文件名记账）——
迁移修正必须以新编号文件交付**。本笔改 051/052 可行仅因采用重放恰好
执行一次新内容,属一次性窗口。

## 记档

- P3:startDailyBillingScheduler 已无调用方（statements 时代停用）,本笔
  仅改签名保编译,属死代码待清理批次。
- 部署提示:升级本版首次启动=最后一次全量重放（30min 超时,advisory 锁）,
  此后重启不再重放、启动加速;重启丢 compact 指针的缺陷根除。

## 测试

- server / agent / internal 三树 vet + test 全绿（server 含
  CT_MYSQL_TEST_DSN 真库,迁移重放与台账契约覆盖）;前端无改动。
