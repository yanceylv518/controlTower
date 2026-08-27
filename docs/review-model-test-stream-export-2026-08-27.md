# 验收记录：模型测试计费口径 + 核对 CSV 流式下载（2026-08-27）

- 范围：`d6f77d1 fix(billing): align model tests and stream review exports`
- 结论：**通过**。交付面两个,均有当日 PROJECT_PROGRESS 条目对应。

## 1. 渠道模型测试计费修正（服务端）

`token_name=模型测试` 的日志清空计费缓存通道（read/write/5m/1h 全置 0）,
保留完整输入 Token;普通请求缓存计费不变。

源码实证（newapi）:
- `controller/channel-test.go:514` 测试日志 `TokenName="模型测试"` 精确
  字符串,判据成立。
- `settleTestQuota`（channel-test.go:543）:
  `quota = round((PromptTokens + round(Completion×CompletionRatio)) × ModelRatio)`
  ——完整 prompt,无缓存减法,无 cache_ratio。
- `buildTestLogOther` 却把 CachedTokens 与 CacheRatio 写入 other——
  「日志带缓存字段但结算不用」的前提逐字成立;CT 原逻辑照常拆缓存会对
  这类行少算（cache_ratio 0.1 折扣被误用）落 mismatch 桶。
- 回归测试:(86+16×5)×10=1660 位级对账;普通令牌缓存通道保留测试钉住。

P3 记档（两条,理论边角,错则落 mismatch 桶无错账）:
- `settleTestQuota` 不乘 group_ratio,CT 重算乘以日志内 group_ratio——
  测试行记录的 group_ratio 恒 1 时无差,若出现 ≠1 的测试行会 mismatch。
- 测试行走 tiered 结算时 newapi 用真实 usage（含缓存变量）,CT 清零缓存后
  tiered 变量 cr=0 可能分叉;「模型测试+tiered 计费」组合罕见。

## 2. 核对 CSV 流式下载（前后端）

- 服务端:异常 CSV 在 BOM+表头后立即 flush,之后每 5000 行分页 flush——
  二十万级数据不再长时间无响应。
- 前端:内部异常/核对差异 CSV 改浏览器原生下载
  （`startBillingFileDownload` anchor 导航）,不再 fetch 全量缓冲成 Blob;
  按钮 loading+防重复点击,1.5s 后释放按钮状态。
- **鉴权核实**:CT 中间件仅对非 GET 强制 X-Requested-With CSRF 头
  （auth/handlers.go:268）;两个下载端点均为 GET,anchor 导航自动携带
  session cookie,鉴权链路不断。
- 记档:流式响应开始后若查询中途出错无法改状态码,浏览器表现为下载失败/
  截断文件,属流式下载固有取舍;原生下载错误不再有 ElMessage 弹层,由
  浏览器下载条呈现。

## 测试

- server 树 vet + test 全绿（含 CT_MYSQL_TEST_DSN 真库）;
  webapp typecheck + build 通过;agent/internal 未触及。
- **模型测试口径影响存量账单:含模型测试请求的账单日重生成后该类行从
  mismatch 桶归位正常单**（与本日其余计费修正同批重生成即可）。
