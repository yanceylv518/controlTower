# 调权中心渠道分组配置实现记录

## 目标

在调权中心渠道列表中直接维护 New API 渠道分组，支持一个渠道配置多个分组组合，并覆盖直连站点和 Agent 管理站点。

## 已实现

- 新增站点级渠道目录接口，读取每个站点最新的 `channel_current`，不受调权基础值接口的启用状态、单模型限制影响；直连站点另读取 New API `/api/group/` 的完整分组列表。
- 新增逐渠道分组编辑入口，只允许选择当前站点 New API 分组列表中的分组、复用已有组合，以及清空全部分组；无直连站点从全渠道快照汇总候选，不允许手工创建分组。
- 使用 New API 现有的逗号字符串合同，例如 `default,vip,fast`；服务端统一去首尾空格、去重，并拒绝逗号空项、控制字符和超过 128 个 Unicode 字符的组合。
- 非空组合中的每个分组都必须已经出现在当前站点的分组目录中；未知分组返回 `400 group_not_found`，不会创建命令或写入 New API。
- 复用 `tuning.manage` 权限；分组修改需要显式 `confirm=true`，并在服务端重新校验渠道属于目标站点。
- 直连站点通过已配置的 New API 管理接口同步写入，成功后立即回写站点内所有 `channel_current` 行。
- 未配置直连的站点创建 `channel.update` Agent 命令；Agent 成功回报后才回写分组，失败不覆盖当前值。
- 命令和操作审计保留操作人、旧分组、新分组及执行结果；直连和队列结果分别返回同步成功或等待执行状态。

## 接口

### 查询渠道

`GET /api/dashboard/tuning/channels?site_id=<site>`

响应中的 `items` 包含 `channel_id`、`channel_name`、`status`、`weight`、`priority`、`models` 和 `group_name`。

### 查询分组

`GET /api/dashboard/tuning/groups?site_id=<site>` 返回当前站点可选的完整分组名称。直连站点从 New API `/api/group/` 读取，无直连站点从完整渠道快照去重得到。

### 修改分组

`PUT /api/dashboard/tuning/channels/{channelID}/group?site_id=<site>`

```json
{
  "confirm": true,
  "group": "default,vip"
}
```

空字符串表示清空分组。非空组合只能使用当前站点分组目录已有的分组名称。直连成功返回 HTTP 200，Agent 队列返回 HTTP 202；格式或未知分组返回 400，站点中找不到渠道返回 404。

## 执行和回写

```text
页面提交
  -> Server 校验站点、渠道、确认项、分组格式和已知分组集合
  -> 直连：GET 渠道 -> PUT group -> 回写 channel_current -> 成功审计
  -> Agent：写入 pending channel.update -> heartbeat 下发 group
             -> Agent PUT 成功回报 -> 完成命令 -> 回写 channel_current -> 审计
```

队列命令 payload 内的 `before_group` 仅供服务端审计使用，不会下发给 New API；Agent 只收到需要写入的 `group` 字段。

## 验证

已通过：

- `gofmt.exe`
- 相关 Go 单元测试：渠道客户端、Dashboard 分组处理、Agent 心跳/执行器、命令回写、直连辅助逻辑
- `go.exe test ./...`
- `pnpm.cmd --dir webapp typecheck`
- `node.exe --test webapp/packages/desktop/tests/*.test.mjs`
- `pnpm.cmd --dir webapp build`
- `git.exe --no-pager diff --check`

已在本地 Docker 测试环境完成真实数据库、直连/Agent 队列和浏览器联调；新增未知分组的数据库集成断言在未设置 `CT_MYSQL_TEST_DSN` 时按仓库约定跳过，单元测试已覆盖相同边界。

## 兼容性说明

- 不新增数据库迁移，复用已有 `channel_current.group_name`、`channel_commands` 和 `operation_audits`。
- 保留原有通用渠道命令接口，并将 `group` 作为可选字段向后兼容扩展。
- 未把分组写入 `channel_base_values`，避免线上渠道属性污染调权基础值。
- Agent 队列功能需要同步升级 Agent；旧版本会忽略未知的 `group` 字段，不应承担新的分组命令。
