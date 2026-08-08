# 验收记录：缓存写默认价终局对齐 + 对账页残差列（2026-08-08）

范围：0ba1cea（对账页显示打磨）+ 212dee9（缓存写默认回归 1.25）。验收环境：Linux，Go 全量 vet+test、vue-tsc、desktop build 绿；**关键声称全部以 new-api 源码实证**（scratchpad/newapi-src）。

## 结论：两笔通过，零缺陷

## 212dee9 缓存写默认价：第三次反转，本次以源码定案

政策沿革：2445cf8 默认 1.25 → 48185bd 用户拍板改 0（"不发明价格"）→ 895feaa 延伸到 CT 配价 → 本笔回归 1.25。**本次与前两次的本质区别：有源码实证**——

- `setting/ratio_setting/cache_ratio.go:167`：GetCreateCacheRatio 未配置返回 `(1.25, false)`；
- `relay/helper/price.go:113`：**计费链路 `cacheCreationRatio, _ =` 忽略 ok 布尔**——未配置模型运行时照收 1.25×，只有价格展示页区分是否显式配置；
- `relay/helper/price.go:37`：`claudeCacheCreation1hMultiplier = 6/3.75 = 1.6`——CT 的 1.6 硬编码与源码常量、生产样本行内 ratio（2/1.25）三方互证。

即 48185bd 的拍板前提（"没配就是不收费"）被源码证伪：newapi 实际一直在收，CT 计 0 属系统性少算——对账页上线后该差以 cache_write_policy 分量暴露，本笔将 CT 与 newapi 实收对齐（FallbackPrice 与 priceWithSnapshotCacheWrite 覆盖均按 input×1.25），对账残差随之归位。测试同步改写自洽。金额为查询时现算，无需升 request_key、无需重生成。

## 0ba1cea 对账页残差列

L1 表加"剩余差额"列 + 公式注释（残差=总差−异常−缓存写），"差额"改"总差额"、"主要分类"改"主要原因"——纯显示，与分解口径一致。

## 记档

- 政策三次反转终局：**以 newapi 实际扣费行为为准**（源码背书），此前"显式计价"记录相应过时——review-anomaly-tz-explicit-cache-write（48185bd）与 review-billing-throttle-writeprice（895feaa）两篇的政策描述以本篇为准；
- 计价页"cache_write_price_configured"标注价值下降（实收不区分配置与否），显示语义可后续再理——P3。

## 部署

无迁移。rc37 不含 2026-08-08 的币种根因修复与本两笔——**建议打 rc38 收齐**（币种修复 + 缓存写终局 + 对账残差列）。

## 追加（同日）：对账页站点货币（ee7962c，验收通过零缺陷）

对账页金额显示接入站点货币（原硬编码 $）：server 复用 currentBillingCurrency + 快照回退 + USD 兜底的降级模式（与我在 b5b5569 立的习惯一致），前端符号+汇率换算齐全，CNY 测试在。冒烟实证：响应带点键形态解析的 ¥/CNY/7.2；1.25 对齐后分解不变量成立（残差在政策切换前后严格不变——两侧同步移动，分解正确性的直接证明）。rc38 不含本笔。
