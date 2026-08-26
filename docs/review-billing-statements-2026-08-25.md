# 验收记录：账单系统重设计——账单任务/可交付账单/大数据生成链路（2026-08-25）

范围：09224fc/cbe2e44/92a77ba/0444633 四笔共 7114 行 111 文件（codex 多轮交付,产品决策全程由用户驱动并记录于 PROJECT_PROGRESS.md——该文档自此为持久上下文,验收对照其决策清单逐项核）。验收环境：Linux,vet+全量三树测试、迁移 048-060 真库 ApplyDir 双重放、typecheck+build、新 server 实跑+页面截图+端到端账单任务实测。

## 结论：通过;一处验收修正（环境敏感测试）;一条 P3;一处烟测环境适配

## 架构核验（对照 PROJECT_PROGRESS 决策清单）

- **任务队列**：GET_LOCK 命名锁+事务内 request_key FOR UPDATE 永久查重+pending≤5 上限——实测创建 user_statement 入队并执行;
- **大数据链路**：(created_at,id) 键集 2000 行/页+页间 500ms;普通请求进磁盘压缩分片（不再写请求台账表）,CT 侧仅累加 compact 日汇总;发布=分片流式出 XLSX/CSV+写后重读 SHA-256+登记成功才切换生效版本（临时文件+rename,失败不覆盖现行,重启可续）;
- **迁移 048-060 十三个**：全部有界（051/052 修复型 UPDATE 带注释,058 cutover 刻意不做启动全表回填——历史非空账单标 needs_regeneration 走正常任务重生成,启动零大扫描）,真库双重放全过,statement 表系+FK ON DELETE CASCADE 建齐;
- **表达式计费**（新依赖 expr-lang/expr v1.17.6）：typed Env+AsFloat64 编译,无 IO 无循环的纯表达式求值,管理员配置面;阶梯/表达式命中层级落明细字段;
- **前端收敛**：账单管理=用户账单/上游账单/账单任务/渠道折扣;新旧任务类型隔离（新界面只认 user/upstream_statement）;任务页三 Tab+队列位置展示（截图核）;
- **重试壳实战验证**：端到端中亲见 attempt=5/退避 2min 的慢而不死行为,烟测 schema 修复后任务自愈续跑。

## 验收修正（我直接修）

NewJob 两测试用 time.Local 造时间——codex Windows(东八区)本机绿,Linux(UTC)按沪时日对齐劈出不同步数即红。改 BusinessLocation 显式时区。**"Codex 在 Windows 漏 Linux 行为"第三次,时区敏感测试一律禁用 time.Local**。

## P3 记档

`parseBillingInputRange` 对纯日期 `to` 按独占瞬间解析,"结束日期为包含关系"的定案由前端 dayAfter(+1 天)补——**绕过界面直接调 /billing/statements 的调用方按包含语义传参会静默丢失最后一天**（同日 from=to 则显式报错）。建议后续:server 对纯日期输入统一+1 天内化包含语义,或 api-contracts 显著标注。

## 烟测环境适配（非代码问题）

烟测 newapi.logs 建于令牌功能前缺 token_id/token_name,已补列——生产 newapi 实有（源码核过）。

## 部署

迁移 048-060 增量+058 有界 cutover;**旧非空账单日启动后标 needs_regeneration,按新任务流程重生成**;下载路由改预生成文件（旧实时扫描路由已移除）。rc67 不含本系列。

## 端到端实测结论（烟测栈,真库真文件）

user_statement 全链路走通：创建入队（accepted）→ runner 拾取执行（1 步完成）→ billing_statements 登记（normal_orders=3 与种子数据吻合,UTC 区间=沪时 8/20 全天）→ 结果接口返回 日×模型×渠道 汇总（**金额 0.476400 与种子 quota/QuotaPerUnit 换算逐位吻合**、折扣 1.0 应用、count_balanced=true、明细 XLSX 文件名已登记）。过程中顺带三次实证"失败任务不占查重键,同参可重建"设计（schema 缺列致前两次失败,重建即重新入队）。旧 generate 任务的历史失败记录与新任务共存无干扰。

## 跟进（1fadf3d,2026-08-26,验收通过零缺陷——容器化前置必需）

Dockerfile 降权前预建 /app/data 并 chown 运行时用户（否则非 root 进程写出档目录必 Permission denied）;compose 加命名卷 ct-billing-data 挂 /app/data——出档文件跨容器替换持久（否则升级镜像即孤儿化全部已登记下载）。路径链核实:WORKDIR=/app+相对 data/billing-files 匹配;命名卷首挂载继承镜像目录属主;file_cleanup runner 兜卷增长。**statements 功能容器部署的前置条件,必须随下一 tag 的镜像构建生效**。
