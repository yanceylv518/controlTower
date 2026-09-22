# Control Tower Deploy

Deployment files in this directory are examples only.

Rules:

- Do not commit real `.env` files.
- Do not store real tokens, webhooks, passwords, or DSNs.
- Use `.env.example` files with placeholders only.
- The Agent reports outbound to Control Tower Server; no Agent inbound port is required.

## 本地使用日志性能数据

在本地 MySQL 测试容器中生成一百万条混合日志，可用于验证使用日志分页、错误码和空输出筛选：

```powershell
pwsh.exe -NoLogo -NoProfile -NonInteractive -File .\deploy\seed-log-filter-load.ps1
```

脚本默认写入 `new_api_mock.logs`，使用 `ct-perf-v1-` 请求 ID 前缀，并生成 70 万条正常请求、10 万条空输出请求和 20 万条错误请求（429、413、503、400、502 各 4 万条）。重复执行不会追加重复数据；需要重建这组数据时使用 `-Reset`。脚本只删除和重建自己的前缀记录，不会清理其他日志。

