# 验收记录：账单读路径绑定任务版本 + 导出错误透传（2026-08-07）

范围：c2ff356（codex 对当日"导出失败/日账单无数据"事件的修复）。验收环境：Linux，Go 全量 vet+test、vue-tsc、desktop build 绿；**首次执行真库端到端冒烟（本批起为资金链路验收标配）**。

## 结论：通过，零缺陷；端到端冒烟全部实证通过

## 改动核验

- **billingJobForRead**：detail/工作簿接受显式 job_id，页面把正在展示的任务 id 传下去——抽屉与导出锁定同一版本，不再与"最新任务"竞态；job_id 校验实例/类型/精确区间，不匹配 → 409（防串版本，Equal 比较时刻与时区无关核过）；
- **detail 无完成任务改 409**（原来静默空列表）——与 workbook/summary 的 409 语义对齐；
- **导出错误透传**：fileDownloadWriter 缓存 ≥400 响应体、解析 error 码——导出任务失败原因从笼统"export generation failed"变具体（**当日事件的直接修复，"409 折叠"P3 就此闭环**）；
- **下载改 blob fetch**：下载失败可弹错误（原 window.location.assign 静默），自定义文件名。

## 真库端到端冒烟（MariaDB 11.4 于 scratchpad，造 4 行日志：anthropic 标记行/openai 缓存行/零 token 异常行/普通行）

1. 迁移 001-038 全量自动执行通过（**035 哨兵版、038 窗口函数在真库实证**）；
2. 生成任务 24 步完成；**泳道拆分逐位核对**：普通输入 898 = 298（anthropic 不减）+ 600（openai 1000−400 减缓存）✓，缓存读 43668、写 2382（全落 1h 档）✓；
3. 异常单：零 token 行正确归异常，**actual_amount=0.001000 = 500 quota ÷ 500000 换算实证** ✓；
4. detail 绑 job_id 返回日明细且与 summary 一致 ✓；假 job_id → 409 ✓；
5. **未生成区间导出任务 error="billing_not_generated"** ——错误透传实证 ✓；正常导出产出合法 xlsx（file 识别 Excel 2007+）✓。

冒烟环境差异声明：MariaDB 11.4 代替生产 MySQL 8（迁移/JSON 函数/窗口函数兼容已实证；生产仍以部署后验证为准）。冒烟栈保留在 scratchpad 供后续批次复用（mariadbd 端口 33061 + ct-server 端口 18090）。

## 记档

- P3：渠道账单页（V4 抽屉/渠道导出）未同步 job_id 绑定——用户页修了渠道页没修，同类竞态在渠道侧仍存在，下次顺手补；
- P3：重生成运行期间导出/明细报"billing_not_generated"——比笼统失败好，但语义上任务明明在跑，理想文案是"生成中 N%"（当日事件里提出的产品改进，仍待做）；
- rc34 不含本笔，下次 tag 收齐。
