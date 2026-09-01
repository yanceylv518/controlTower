# 验收记录：缓存溢出遗留结算 + 待复核账单下载解锁（2026-09-01）

- 范围：`41869c6 fix: reconcile cache overflow billing`
- 结论：**通过**。交付面两个,第二个属产品语义变更且无 PROJECT_PROGRESS
  条目,**待用户确认**（见末尾）。

## 1. 缓存溢出遗留结算兼容（billing 核心）

e6b9758 按现版 newapi 源码把缓存溢出行（cache>prompt 的 OpenAI 语义行）
钳位到 0 计算。生产核对差异样例证明**旧版 newapi 不钳位**——直接对负的
普通输入通道计价:样例 SourcePrompt=121854/cache=121856/completion=1151,
(-2 + 121856×0.1 + 1151×5)×10 = 179386 与实扣 quota 位级吻合;钳位算法
得 179406,差 20 超容差,故此前该类行滞留 mismatch 桶。

修正:`applyCacheOverflowAdjustment` 仅作用于重算校验路径——展示/统计的
普通输入通道保持钳 0（InputAmount=0.000000 测试钉住）,重算 total 追加
带符号调整（ordinary×输入单价）。守卫:仅 token 模式、非 anthropic、
非多模态（图片/音频通道会合法重叠,走各自路径）、normalized prompt=0 且
raw ordinary<0 时生效。

取舍记档:若某行来自**现版**（源码确认钳位）newapi 且缓存溢出,调整后
重算会比实扣少 |ordinary|×ratio 落 mismatch 桶——与修正前方向互换。以
生产实证数据优先(旧版行实存,现版钳位行未见实例),错向仍是安全网不错账。

## 2. 待复核账单 ZIP 下载解锁（产品语义变更,待确认)

- 原行为:mismatch_rows>0 → 下载接口 409、前端按钮禁用,「待复核不可
  下载正式 ZIP」。
- 新行为:ZIP 始终可下载,存在核对差异时包内**附带 核对差异.csv**;
  预览弹层与列表按钮解禁,文案改「下载包会附带核对差异 CSV;交付前请
  先复核」。核对差异 CSV 写出逻辑抽 `writeReconciliationCSVData` 与独立
  下载端点共用。
- **用户已确认此变更系本人指示（2026-09-01 收档）**:「复核未清不可交付」
  硬闸调整为「可下载,包内附差异 CSV,交付前人工复核」。
- P3 观感残留:核对差异 tab 内 alert 仍写「不能直接交付」,与新文案并存,
  建议下批统一;PROJECT_PROGRESS 条目建议 codex 补记。

## 测试

- server 树 vet + test 全绿（含 CT_MYSQL_TEST_DSN 真库）;新增遗留结算
  位级回归（179386）;webapp typecheck + build 通过;agent/internal 未触及。
- 含缓存溢出行的账单日重生成后该类行从 mismatch 桶归位正常单。
