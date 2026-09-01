# 验收记录：阶梯计费嵌入时区数据（2026-09-01）

- 范围：`7626dfc fix: embed timezone data for tiered billing`
- 结论：**通过，零缺陷**。

## 问题与修复

阶梯计费表达式的 `hour("Asia/Shanghai")` / `weekday("Asia/Shanghai")` 走
`time.LoadLocation` 按名加载时区——容器基础镜像无 tzdata 时加载失败,
阶梯行整批评估出错落异常桶。CT 主链路一直没炸是因为 `BusinessLocation`
用 `time.FixedZone`（不依赖时区库),缺口仅在按名加载的表达式路径。

修复:`_ "time/tzdata"` 匿名导入,把 IANA 时区库嵌入二进制（Go 标准机制,
体积 +~450KB)。作用于整个 server 进程,任何 LoadLocation 均自足,不再
依赖镜像内 /usr/share/zoneinfo。

## 测试

- 新增回归:真实形态的峰谷表达式在上海时间周二 15:55 求值命中「高峰时段」,
  quota=158 位级对账。
- server 树 vet + test 全绿（含 CT_MYSQL_TEST_DSN 真库）;agent/internal/
  前端未触及。

## 备注

- 仅 server 二进制受益;agent 不用按名时区,无需跟进。
- 若生产曾见「阶梯计费行大量落异常桶且原因指向时区/表达式求值」,升级后
  重生成对应账单日即归位。
