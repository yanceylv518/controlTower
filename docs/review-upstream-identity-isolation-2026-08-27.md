# 验收记录：上游账单身份字段隔离（2026-08-27）

- 范围：`600ff21 fix(billing): remove user identity from upstream statements`
- 结论：**通过**。单一交付面,有当日 PROJECT_PROGRESS 条目对应。

## 变更内容

上游账单定位为「上游→渠道→日期→模型」的对外结算对账交付物,全链路剔除
用户/令牌身份字段:

- 详情预览（内部异常/核对差异 tab）:上游任务按 渠道 ID+渠道名称 展示,
  不再返回 username/token;用户任务保留令牌并补 token_id。两类都补
  上游 Request ID。
- 内部异常 CSV / 核对差异 CSV:按任务类型分表头——上游=请求时间/Request
  ID/上游 Request ID/渠道 ID/渠道名称/模型;用户=令牌 ID/令牌/模型序。
- 渠道每日逐请求 CSV（上游交付物）:「用户/令牌」列改「上游 Request ID/
  渠道」。
- RequestDetail 增 UpstreamRequestID 字段并在扫描落盘时填充。
- 前端两个 tab 列随任务类型切换（upstream 显渠道,user 显令牌）,新增
  上游 Request ID 列。

## 核对要点

- 用户账单链路不受影响:用户日 XLSX 生成器未触及,令牌维度保留。
- 逐请求追踪能力不降级:渠道+Request ID+上游 Request ID 足以在 newapi
  侧回查,身份字段只是从对外交付物中移除,CT 库内明细仍存全量字段。
- 表头顺带统一:「异常记录参考金额」→「参考重算金额」（两分支同改）。

## 测试

- server 树 vet + test 全绿（含 CT_MYSQL_TEST_DSN 真库,店内核对/异常
  查询字段映射由编译与真库测试覆盖）;webapp typecheck + build 通过;
  agent/internal 未触及。

## 记档

- **历史静态账单文件需重新生成才采用新字段结构**:旧渠道日 CSV 仍是
  用户/令牌列;旧 spool 分片无 UpstreamRequestID,重生成前该列为空。
  与本日计费口径修正同批重生成即可一并归位。
