# 验收记录：对账单分项下载 + 通知设置初次加载（2026-09-10）

- 范围：`03e4981 feat(billing): download statement summary and daily details separately`、
  `1f79d46 fix: load notification channels when opening settings`（合并 `5d78296`）
- 结论：**两笔均通过，零缺陷**。

## 03e4981 对账单分项下载
- 预览响应新增 `daily_files`（按对账单登记的每日明细文件列表，含展示文件名）；
  `download=1&export=summary` 单独下载主账单 xlsx；`export=daily&day=YYYY-MM-DD` 按对账单
  自身登记的文件下载当日明细（不走当前活动账单），路径限定在账单文件根内；原整包 zip 保留。
  权限检查（按库中任务类型）在所有导出分支之前。
- 实证：`go vet`、`go test ./...` 全绿（新增下载测试）；`pnpm typecheck`/`build` 通过。烟测：
  预览返回 `daily_files`；summary 导出为含 3 张表的 xlsx 并带中文文件名；daily 导出返回登记的
  明细 xlsx；不存在的日期 404；`day=../etc` 400；整包 zip 仍可下载。
- P3：每日明细按登记表 `bill_day` 匹配与命名，rc83 之前登记的旧对账单日期可能偏一天
  （既知旧数据问题，重生成自愈）；烟测这份旧对账单即显示 08-19。

## 1f79d46 通知设置初次加载
- 打开通知设置页时主动加载通知渠道列表（此前需手动刷新才显示），空态文案简化。
  typecheck/build 通过，渠道接口 200。

## 部署
- 纯 server + 前端，无迁移，未动 agent。rc104 打在 05a5fad 不含两笔，上线需重打 rc105。
