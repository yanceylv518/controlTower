# v2.0-B2 生产上线 Runbook

执行者：用户本人（碰生产步骤逐一确认）。拓扑：new-api ×2（阿里云杭州 / 腾讯云香港），Control Tower 新购腾讯云 Ubuntu 服务器，Compose 自建 MySQL。

## 拓扑要点（开工前读一遍）

1. **流量方向只有 Agent → Server 出站**（gzip 上报，每 30 秒几 KB）。杭州 Agent 到腾讯云服务器如跨境（香港区），偶发抖动由 Agent 本地缓冲+重试兜底，钉钉告警链路完全独立不受影响——架构上天然容忍。
2. **服务器地域二选一**（下单前定）：**香港**——免备案、离港区 new-api 近，但你从内地开 Web 面板偶尔慢；**内地（如广州/上海）**——面板访问快，用 `IP:8080` 直连实践上无需备案（不绑域名不走 80/443）。两者皆可，Agent 端无差别。
3. **安全基线（必做其一，推荐 A）**：面板与上报走公网明文 HTTP，必须收口——
   - **方案 A（零成本，推荐先用）**：腾讯云安全组仅放行 8080 给三个来源——杭州 new-api 公网 IP、香港 new-api 公网 IP、你的办公/家庭 IP（变动时改安全组）。22 端口同理收紧。
   - **方案 B（有域名再加）**：域名解析到服务器 + Caddy 反代自动 HTTPS，8080 只监听内网、443 对外。可上线后随时补，不阻塞本次。

## 阶段 1：服务器初始化（新腾讯云 Ubuntu，~15 分钟）

```bash
# 1. 装 Docker（官方脚本）
curl -fsSL https://get.docker.com | sudo sh
sudo usermod -aG docker $USER && newgrp docker

# 2. 拉代码并部署（自建 MySQL 随 Compose 起）
git clone https://github.com/yanceylv518/controlTower.git && cd controlTower/deploy/compose
cp .env.example .env
vim .env   # 逐项填：MySQL 密码、CT_DATABASE_DSN、CT_AGENT_TOKEN（全局回退，随机长串）、
           # CT_DASHBOARD_TOKEN、CT_AGENT_TOKEN_PEPPER、CT_ADMIN_USERNAME/CT_ADMIN_INITIAL_PASSWORD、
           # CT_PUBLIC_BASE_URL=http://<服务器公网IP>:8080
docker compose up -d --build   # 首次构建 ~5 分钟

# 3. 验证
curl -s http://127.0.0.1:8080/healthz        # {"status":"ok"} 之类
docker compose logs server | head            # 看到迁移完成 + initial admin created + listening
```

浏览器开 `http://<服务器IP>:8080` → 登录 → 修改初始密码（设置页）。

## 阶段 2：建实例、拿 token（Web 上操作，~5 分钟）

实例管理页创建两个实例（**Token 弹窗只显示一次，先复制存好**）：

| instance_id | 对应 |
| --- | --- |
| `inst-hangzhou` | 阿里云杭州 new-api |
| `inst-hongkong` | 腾讯云香港 new-api |

## 阶段 3：Agent 接入（一台一台来，先香港后杭州）

对每台 new-api 服务器（香港这台如尚无 Agent 则走 `install-agent.sh` 全新安装，配置含下方新增三行）：

```bash
# 1. 下载 Release 的 Agent 包（v2.0.0-rc1，含 v1.0.7 与慢返回等全部功能）
wget https://github.com/yanceylv518/controlTower/releases/download/v2.0.0-rc1/control-tower-agent-v2.0.0-rc1-linux-amd64.tar.gz
tar xzf control-tower-agent-v2.0.0-rc1-linux-amd64.tar.gz && cd control-tower-agent-*/

# 2. 升级二进制（已有 Agent 的机器）
sudo systemctl stop control-tower-agent
sudo cp control-tower-agent /usr/local/bin/control-tower-agent

# 3. 配置追加双模式三行（钉钉配置一行不动！）
sudo vim /etc/control-tower/agent.config
#   CT_SERVER_URL=http://<CT服务器IP>:8080
#   CT_AGENT_TOKEN=<该实例的 token>
#   CT_INSTANCE_ID=inst-hangzhou   ← 改为与实例一致（原值若不同必须改，网关校验不匹配会 403）

# 4. 起服务并验证
sudo systemctl start control-tower-agent
journalctl -u control-tower-agent -f
#   预期：版本行显示 v2.0.0-rc1；审计日志正常；无 401/403/连接错误
```

**每台接入后立即在 Web 核验**：实例管理页该实例 Agent 显示在线、心跳更新；总览切到该实例有指标流入（等 1~2 分钟聚合）。杭州那台顺带观察跨境上报稳定性（journal 无持续报错即可）。

**回滚预案**（任一台异常）：注释掉新增三行、重启 Agent → 立即回到纯钉钉独立模式，零损失。

## 阶段 4：验收与收尾

1. **双链路确认**：钉钉群告警照常（可临时调低慢阈值触发一条）；Web 告警中心同步可见 Server 端规则告警。
2. **观察期 3~7 天**：每天看一眼——两实例在线状态、指标连续性（杭州跨境是否有断流）、`docker compose logs` 无异常、磁盘增长正常（保留清理在跑）。
3. 观察结论反馈给 Claude → 写入迭代记录 v2.0 章节 → 打正式 tag `v2.0.0`（同代码重新出正式产物）→ **v2.0 发布完成**。

## 故障速查

| 现象 | 排查 |
| --- | --- |
| Agent 日志 401 | token 填错或实例被停用；403 instance_mismatch = CT_INSTANCE_ID 与 token 不匹配 |
| Web 打不开 | 安全组 8080 未放行你的 IP；`docker compose ps` 看容器状态 |
| 实例显示离线 | Agent 端 journal 看连接错误；跨境抖动会自动重试补报 |
| 面板无指标 | 等聚合周期；确认 Agent 日志有 report 成功；实例筛选是否选对 |
