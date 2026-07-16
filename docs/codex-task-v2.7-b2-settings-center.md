# Codex 任务：v2.7-B2——系统设置中心（数据保留/告警阈值 Web 可配）

用户反馈：设置页只有改密码,数据保留等系统配置无处可调。本批建设置存储与设置页第一期。**依赖 v2.7-B1 无;可并行。**

**文末自查清单粘贴进 commit message;禁止 force push;Linux 跑全量测试。**

## 设计

- **存储**：迁移 `011_system_settings.sql`——`system_settings(setting_key VARCHAR(128) PK, setting_value VARCHAR(255), updated_at, updated_by)`,钉 ENGINE/CHARSET/COLLATE;**禁止任何表重建型 ALTER**（见 010 教训与反向断言测试模式）;
- **优先级语义**：库中有值用库值,无值回退 env,env 也无则内置默认——`settingsProvider`（60s 缓存,参考 nameResolver 模式）,各 runner/规则每周期读 provider,**改配置无需重启**;
- **第一期可配项**（键名与现 env 一一对应,表单按分区展示）：
  - 数据保留：明细天数（logs/样本/metric_1m/nginx 两表）、metric_5m 天数、运行态天数（对应 CT_RETENTION_* 三档）;
  - 告警阈值：实例离线秒数（CT_OFFLINE_ALERT_SECONDS,v2.6-B1 引入）、CPU/内存/磁盘 warn/crit 六项、错误率 warn/crit、**P95 warn/crit（顺手解决"5s/10s 对流式偏敏感"——默认值不变,可调了）**;
  - 通知：告警通知开关（总开关,默认开）。
- **校验**：保留天数 1~365;阈值百分比 1~100 且 warn<crit;P95 秒 0.5~600;非法 400 带字段错误;
- **审计**：每次修改写 operation_audits（operation_type=settings.update,before/after 摘要）;
- **API**：`GET/PUT /api/dashboard/settings`（protect,PUT 走 session actor）;响应含每项的 `value/source(db|env|default)/default`——页面能看出哪些被改过;
- **Web 设置页**：分区表单（账号/数据保留/告警阈值/通知）,展示生效值与来源标签,保存后即时生效提示;改密码功能保留。

## 接线点（逐个核对,不得遗漏）

retention runner（三档天数）、offline 规则、appendServerMetricAlerts（资源阈值）、appendMetricAlerts（错误率/P95）、notification dispatch（总开关）。各接线点单测:库值覆盖 env、删除库值回退 env。

## 验证要求

1. 全量测试绿;011 迁移 sanity + 无重建语句断言;
2. 手工：改保留天数 → 观察下轮清理日志用新值;改 P95 阈值 → 告警按新阈值触发;删库值 → 回退 env;全程不重启 Server;
3. 交付说明含可配项清单与默认值表。

## 交付前自查清单（填好粘贴进 commit message）

- [ ] 库值>env>默认三级回退有测试;改配置不重启即生效
- [ ] 五个接线点全部走 provider 并各有覆盖测试
- [ ] 校验拒绝表齐全（含 warn<crit）;修改写审计
- [ ] 011 无重建语句(反向断言);API 响应含 source 标签
- [ ] 一个 commit：`feat(server,web): system settings center with live reload (v2.7-B2)`

## 明确不做

Agent 侧配置下发（Agent 仍用本地 config,另议）;通知模板/静默时段;多用户权限分级;设置项历史版本回滚（审计里可查即可）。
