# rc20 渠道当前状态迁移

rc20 将渠道数据从“每次采集追加一份历史快照”改为“每个实例、每个渠道只保留一行当前状态”。

首次启动时，Server 会执行以下步骤：

1. 创建 `channel_current`；
2. 从 `channel_snapshots` 提取每个 `(instance_id, channel_id)` 最新的一行；
3. 最新状态迁移成功后启动 HTTP 服务；
4. 后台每批删除 5000 条已经由 `channel_current` 覆盖的旧快照，批次间隔 200ms；
5. Agent 后续上报只 UPSERT `channel_current`，不再产生渠道历史快照。

删除条件要求 `channel_current.captured_at >= channel_snapshots.captured_at`。未成功迁移到当前状态表的渠道历史不会被清理。清理发生错误时任务停止，剩余数据会在下次 Server 启动后继续处理。

## 升级影响

- 首次启动需要扫描旧快照索引。历史表较大时，Server 启动时间会比普通升级长；迁移最长允许 30 分钟。
- 页面、渠道名称解析和调权中心改读 `channel_current`，不再扫描历史表。
- `latest_only=false` 兼容参数仍可使用，但 rc20 只返回当前状态。
- 历史快照被永久删除，升级前必须完成数据库备份。
- 清理旧行不会立即缩小 InnoDB 表空间文件。不要在业务时段执行 `OPTIMIZE TABLE channel_snapshots`。

## 部署后验证

```sql
SELECT instance_id, COUNT(*) AS channels
FROM channel_current
GROUP BY instance_id;

SELECT COUNT(*) AS remaining_history
FROM channel_snapshots;
```

查看后台进度：

```bash
docker compose logs -f server | grep 'channel snapshot history cleanup'
```

完成日志示例：

```text
channel snapshot history cleanup complete rows=47563143
```
