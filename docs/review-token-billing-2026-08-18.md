# 验收记录：用户账单按令牌分组（2026-08-18）

范围：e401d68+e3a6bd9+784cc84（codex 按批次 codex-task-token-billing.md 交付,自查清单已贴全）。验收环境：Linux,vet+全量三树测试、真库集成（046 经 ApplyDir 建表核实）、vue-tsc、desktop build 全绿。

## 结论：通过,零缺陷;一条记档

## 交付面对批次逐项核

全部指令内：①046 单语句 CREATE(契约+真库);②扫描投影补 token 两列/PagedLogRecord 扩字段/request_key 未动/billing_daily_versions 结构未动;③小时聚合按令牌键并行累计,AppendBillingHour 同事务双表累加写(原子性测试在);④读接口 tokens+tokens/daily(读闸 generate job 409 一致,非 admin 站点+用户双闸)+token_data_missing 标记;⑤明细仅下载——异步导出任务(Kind=token)内部窗转 id+token 过滤分页扫,PagePause 页间歇接入;⑥重试壳 ReadPageWithRetry 三处接入(生成扫描/工作簿/令牌导出),退避序列+10min 预算+>10s 慢页日志+context 取消即停,三态测试全;⑦守恒断言(令牌行合计=用户行合计);⑧令牌计价复用 BuildSummary 于令牌分片(同价源);⑨抽屉"按令牌"Tab 上下结构照批次布局;⑩部署说明文档含重生成要求。

**784cc84 导出工件复用**：同指纹(操作者+格式版本+kind+参数)且绑 job 的导出复用既有文件,无 job 导出加随机盐永不复用——判定为导出机件指令内实现细节,正确性护栏齐;记档:job 绑定缓存下改 CT 配价不重生成则命中旧导出——与 184ad75 时代既有记档行为同型,重生成即失效,接受。

## 部署

迁移 046 增量。**部署后需重生成账单一次,令牌月/日账单才有数据**（旧账单显示引导条;明细下载不依赖重生成）。rc62 不含本批;**rc63 应含:上游 key 账单+渠道筛选+令牌账单全套**。
