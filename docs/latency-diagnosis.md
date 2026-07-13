# 延时分诊手册：new-api 日志显示延时大、上游自报很小时怎么查

背景：new-api 的 `use_time` 是**全链路总时长**（含排队、建连、跨境传输、内部重试的全部尝试、流式响应的完整传输期、写库收尾），上游只统计自己的处理段——两边的账本来就量的不是同一段。本手册给出切账方法与排查 SOP。

## 1. Nginx timing 日志（已在两台 new-api 前启用）

生产采用的格式（名 `timed`）：

```nginx
log_format timed '$remote_addr - "$request" [$time_local] '
                 'status=$status rt=$request_time '
                 'uct=$upstream_connect_time '
                 'uht=$upstream_header_time '
                 'urt=$upstream_response_time '
                 'bytes=$body_bytes_sent req_len=$request_length';
# 代理 new-api 的 server/location 内：
# access_log /var/log/nginx/newapi-timing.log timed;
# 记得配 logrotate；nginx -t && nginx -s reload 生效
```

字段语义（Nginx 的 upstream = new-api）：

| 字段 | 含义 | 用途 |
| --- | --- | --- |
| `rt` | 收到客户端请求 → 响应最后一字节发完 | 全链路总时长 |
| `uct` | 与 new-api 建连耗时 | 本机连接层异常 |
| `uht` | 收到 new-api **首字节**耗时 | **金指标 TTFT**：排队+new-api 处理+上游首响应，不受流式传输拖累 |
| `urt` | 收完 new-api 响应总耗时 | new-api 总耗时的第三方计时（可与 use_time 对账）；**注意 new-api 内部重试时 Nginx 只见一次 upstream，重试时间全在 urt 内**；Nginx 自身 upstream 重试时该字段为逗号分隔多值 |
| `bytes` | 响应体大小 | `bytes ÷ (urt−uht)` ≈ 传输吞吐，区分"链路差"与"响应大" |

**分诊公式**：

```
rt − urt        ≈ 客户端网络段（大 → 客户端侧/入站链路）
uht             ≈ 首字节前（大 → new-api 内部/到上游前段/重试）
urt − uht       ≈ 响应传输段（大 → 流式长输出或出站链路差）
```

## 2. 现场分析命令（在 Nginx 机器上直接跑）

```bash
LOG=/var/log/nginx/newapi-timing.log

# ① 最慢 TOP20（按 rt），带全部分段
grep -o 'rt=[0-9.]* uct=[0-9.-]* uht=[0-9.,-]* urt=[0-9.,-]*' $LOG | sort -t= -k2 -rn | head -20

# ② 慢请求归因计数：rt>10s 里首字节慢 vs 传输慢各多少
awk 'match($0,/rt=([0-9.]+)/,r) && r[1]>10 { match($0,/uht=([0-9.]+)/,h);
     if (h[1]>5) a++; else b++ } END{ printf "首字节慢(new-api/上游前段): %d\n传输段慢(流式/链路): %d\n",a,b }' $LOG

# ③ 5xx 与 504 即时清点
grep -oE 'status=5[0-9]{2}' $LOG | sort | uniq -c
```

## 3. 排查 SOP

1. **Web 分诊**（Control Tower 看板）：单渠道慢→渠道/上游链路或重试；全渠道慢+CPU/Load 同步高→本机资源；全渠道慢资源正常→DB 收尾/出口网络/连接池；仅流式慢→传输段。
2. **Nginx 日志归因**（上面命令②）：首字节慢还是传输慢，直接定段。
3. **重试掩盖验证**：延时大的请求拿 request_id 查 new-api logs 表，同 request_id 多条（失败+fallback 成功）即实锤——use_time 含全部尝试，上游只看到成功那次。
4. **网络基线对照**（new-api 服务器上）：
   `curl -o /dev/null -sw 'DNS:%{time_namelookup} 连:%{time_connect} TLS:%{time_appconnect} 首字节:%{time_starttransfer} 总:%{time_total}\n' https://上游域名/v1/models`
   整链耗时 − 网络基线 − 上游自报 ≈ new-api 内部开销。
5. **本机取证**：`docker stats`（new-api 容器资源）、`ss -s`（连接堆积）、MySQL 慢查询（写日志/配额收尾）。

## 4. 监控衔接（2026-07-13 决策：分析型数据，不发钉钉）

- **已有告警**：慢返回窗口告警（渠道/客户 10 条中 3 条 ≥120s，流式独立阈值）；Server 端 P95/资源告警。timing 数据**不接**这套消息链路。
- **规划（批次 `codex-task-v2.2-b1-nginx-timing-analytics.md`）**：Agent tail timing 日志 → 分钟桶聚合（TTFT/传输段 p50/p95、5xx/504 计数、慢请求"首字节主导 vs 传输主导"归因计数）+ 每桶 Top5 慢样本 → 上报入库 → Web「延时分诊」页：归因卡（本手册命令② 的自动化版）、三张趋势图、慢样本表。
- **失效安全要求**：`CT_NGINX_ACCESS_LOG` 未配置 → 该模块整体不启动；配置了但文件缺失/无权限/格式对不上 → WARN 一条后照常运行并重试，绝不报错退出、不影响既有采集与告警（详见 design-v1.1-early-warning.md 信号 E）。
- 网关开销分解探测（经 new-api 整链耗时 − 无 key 的 TCP/TLS 握手网络基线）仍归挂起的 v1.1 探测批次。
