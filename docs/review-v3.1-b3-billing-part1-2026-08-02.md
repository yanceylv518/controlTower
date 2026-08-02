# 验收记录：v3.1-B3 账单（分批交付）（2026-08-02）

范围：ccb6af4（自述"start"，**部分交付**）+ dad1dbe/5d53303/a802ba7/cdaa548 四笔修补。验收环境：Linux，Go 1.24.5 + Node 22（typecheck+build）。

## 结论：已交付部分通过零缺陷；批次未完成，续交清单见下

### 已交付且质量达标

- **025 迁移**：五表齐全（billing_daily 含分组+档位维度、计价表三键、倍率表、ratio/余额快照表），迁移契约测试在（billing_migration_test）；
- **金额计算器**（server/internal/billing）：**用户示例 $0.005828 逐位断言测试在**；缓存从输入扣除、生效日期先于档位的取价顺序、档位表校验（首档 0/严格递增）各有测试；
- **存储层**：价格/倍率 CRUD、ReplaceBillingDay（整日替换=天然幂等）、ratio/余额快照写入——地基完整；
- 修补四笔：viewer 站点选择器锁定 scope 站点（前端纵深，服务端本已强制）、用户页分页与配额显示修正、客户选择器提速、日志明细页字段与日期区间完善。全量测试 + typecheck + build 绿。

### 续交清单（批次剩余部分，按批次文件原文执行）

1. **日切三合一 runner**：每日定时调度，日志聚合按小时 24 分段（含阶梯 CASE 分档、分组维度），调用已有的快照与 ReplaceBillingDay 写入；单站点失败隔离；
2. **回填接口**（按天分段+限速+审计）；
3. **账单 API**：summary（分页/搜索/按消费额降序/合计行独立/**结果缓存至下次日切**）与 detail；金额来源标注 ct|newapi、quota 对照列、未配价回退 ModelRatio 现算（读 CT 快照，**零 newapi 读路径断言**）；CSV 两档（BOM、from/to 区间）；
4. **中央白名单闸扩入账单路径**（viewer GET）+ scope 越权测试三处；
5. **账单页面 + 计价配置界面**（含"从 newapi 导入价格"回填）+ 菜单接线；
6. api-contracts.md 更新；自查清单随最终 commit 补贴。

## 第二部分验收（2026-08-02 追加，b177127）：通过，零缺陷

- **日切三合一 runner**：24 小时分段拉取明细在 Go 内聚合（阶梯必须逐请求判档，此路径正确）；SelectPrice 按"当日生效价表 + 请求 prompt tokens"取档；聚合后整日替换写入 + ratio 快照 + 余额快照一体完成；02:00 后触发、每站点每日一次、小时级重试、单站点失败隔离（有测试）；重启后内存记账丢失仅导致无害重跑（整日替换幂等，有测试）；
- **回填接口**：按天分段 + 限速 + 审计；viewer 经中央闸（非 GET 一律 403）天然无法触达；
- 测试覆盖三关键行为：24 段与判档先于聚合、幂等、站点隔离；全量测试 + typecheck + build 绿。
- 记档（P3）：小时明细查询无行数上限（当前量级每小时万级行无碍，日志量若上一个数量级需加流式/分批，记录在案）；cache tokens 自 logs.other JSON 解析（与生产实测字段位置一致）。

## 第三部分验收（2026-08-02 追加，4a4ab07/52752f1/403af91 + 7130e35）：API 内核通过

- **报表引擎**（billing/report.go）：CT 价优先、未配价按 ratio 快照现算（ModelRatio/Completion/Cache/GroupRatio + 站点 QuotaPerUnit，decimal 运算）、price_source 标注、未定价模型清单——回退链与设计一致；**读路径纯 CT 存储**（summary/detail 仅依赖 Store，结构上零 newapi）；
- **summary/detail**：viewer scope 在 handler 层再次强制（站点匹配+用户集合，与闸构成双层）；分页/合计/data_through；CSV 两档在；
- **计价/倍率配置 API** 就位；viewer 客户授权可编辑（7130e35）；全量测试 + typecheck + build 绿。

## 剩余（第四部分待续交，收尾清单）

1. **中央闸白名单扩入账单 GET 路径**（当前 viewer 被默认拒绝挡住——fail-closed 安全但功能不可用）+ viewer 越权测试；
2. summary **结果缓存至下次日切**（现每请求现算）；
3. CSV 补 **UTF-8 BOM**（防 Excel 乱码）；
4. **"从 newapi 导入价格"** 接口与按钮；
5. **账单页面 + 计价配置界面** + 菜单；
6. api-contracts.md；自查清单随最终 commit 补贴。

## 部署

前两部分随下次发布无害（runner 在无价格配置时照常聚合，tier 全 0，金额层后补）；功能整体随 B3 完成后 rc24。
