# 验收记录：图片日志路径语义修正（2026-08-27）

- 范围：`451d244 fix(billing): distinguish image billing paths`
- 结论：**通过**，附一条 P3 覆盖缺口记档（见下）。系 f09b7d5 图片计费的再修正。

## 问题

f09b7d5 把无显式 image 键的 `image_output` 一律归入图片输入通道。生产钱包
样例证伪了「一律」：`billing_source=wallet`、admin_info 无
`usage_billing_path` 的行（旧版 newapi），`image_output=5688` 是诊断元数据，
图片用量仍在普通输入通道内——quota=44206 位级验证
（(1603+2457.6+360)×10）确认 newapi 未拆图片。CT 照拆会虚增账单
（拆后重算 85056,落 mismatch 桶）。

## 修正

- 投影补 `$.admin_info.usage_billing_path` 标量（不传完整 admin_info,
  投影契约测试同步收紧为禁止 `'admin_info',JSON_EXTRACT` 整块传输）。
- `image_output`→ImageInput 的规范化仅在 `usage_billing_path=upstream`
  时生效;显式 `image_input`/`image_tokens`/`image_output_tokens` 键不受
  路径限制,始终权威。
- 新增钱包生产样例回归测试:元数据不成通道、prompt=1603、quota=44206
  位级对账;既有图片测试样例补 upstream 路径标记。

## 源码实证

- newapi 现源码 `image_output` 唯一写入点在计费路径
  （text_quota.go:486,会拆图片）;钱包样例无 `usage_billing_path` 键
  而现版 `appendUsageBillingPathForLog` 无条件写入——证明该行来自旧版
  newapi,旧版该键确为纯诊断。两版本行为由路径键存在与否区分,判据成立。
- `usage_billing_path` 取值集合:local/upstream/openai/anthropic/gemini
  （含 _estimated 变体）。

## P3 覆盖缺口（记档,不阻验收）

newapi 的 BillingUsage 转换路径同样可产生计费图片通道:
`usageFromGeminiBillingUsage` 按 IMAGE modality 累加
`PromptTokensDetails.ImageTokens`,`usageFromOpenAIBillingUsage` 整结构体
拷贝也可携带——这些行路径值为 `gemini`/`openai`（或 `_estimated`）,
newapi 拆图片计费,CT 现守卫精确匹配 `upstream` 不拆。后果有界:该类行
quota 对不上落 mismatch 异常桶（实扣保留,无错账;且 image_ratio=1 时拆与
不拆算术等价,仅 ratio≠1 才显差）。**若部署后重生成仍见图片行滞留
mismatch 桶且其路径值为 openai/gemini,守卫应扩为「路径非空且非 local」**。

## 验证与测试

- server 树 vet + test 全绿（含 CT_MYSQL_TEST_DSN 真库）;agent/internal
  未触及;前端无改动。
- 部署观察点:含图片账单日重生成后,upstream 路径图片行应归位正常桶;
  rc72 历史样例（quota=191388）依赖其生产行确带 `upstream` 路径标记
  （测试按此假设钉住）,若重生成后该类行仍 mismatch,按上条扩守卫。
