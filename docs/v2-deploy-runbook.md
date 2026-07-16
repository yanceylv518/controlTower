# v2.0-B2 生产上线 Runbook（手把手版）

**拓扑**：Control Tower 服务器 = 腾讯云上海 Ubuntu（下文占位 `CT_IP`，替换为实际公网 IP）；new-api ×2 = 阿里云杭州（`HZ_IP`）、腾讯云香港（`HK_IP`）。数据库 = CT 服务器上 Compose 自建 MySQL。

**原则**：一次只做一步，每步有“预期结果”，不符就停止并检查输出。企业微信告警切换前先直测新机器人，接入动作可随时回滚。

---

## 阶段 0：购机与安全组（腾讯云控制台，~15 分钟）

### 0.1 购买服务器

- 地域：**上海**；镜像：**Ubuntu 22.04 LTS 或 24.04 LTS**；
- 规格：2 核 4G 起步足够（Server+MySQL+面板都很轻）；系统盘 ≥40G；
- 公网：分配公网 IP，**计费方式选按流量**（Agent 上报是入方向不计费，出方向主要是看板访问，月流量费通常很低）；峰值带宽设 20~50Mbps（看板秒开）；控制台顺手设一个流量费用告警（如月超 5 元提醒）。

### 0.2 查你自己的公网 IP（配安全组用）

本机浏览器打开 `https://ifconfig.me` 或终端 `curl ifconfig.me`，记下（下文 `MY_IP`）。注意家庭宽带 IP 会变，变了就来控制台改这条规则。

### 0.3 配置安全组（控制台 → 云服务器 → 安全组 → 新建）

入站规则**只放这四条**，其余默认拒绝：

| 协议端口 | 来源 | 用途 |
| --- | --- | --- |
| TCP:22 | `MY_IP/32` | 你 SSH 管理 |
| TCP:8080 | `MY_IP/32` | 你开 Web 面板 |
| TCP:8080 | `HZ_IP/32` | 杭州 Agent 上报 |
| TCP:8080 | `HK_IP/32` | 香港 Agent 上报 |

出站：默认全放行即可（Server 不主动外连，出站仅系统更新）。绑定到新购主机。

**检查点**：手机 4G（非 MY_IP 网络）访问 `http://CT_IP:8080` 应**连不上**——白名单生效。

---

## 阶段 1：CT 服务器部署（SSH 到 CT_IP，~20 分钟）

### 1.1 装 Docker

```bash
curl -fsSL https://get.docker.com | sudo sh
sudo usermod -aG docker $USER
exit   # 重新 SSH 登录使 docker 组生效
```

重新登录后验证：

```bash
docker version
```

**预期**：Client 与 Server 版本都打印，无 permission denied。（国内拉镜像慢可配置腾讯云镜像加速：`/etc/docker/daemon.json` 加 `{"registry-mirrors":["https://mirror.ccs.tencentyun.com"]}` 后 `sudo systemctl restart docker`。）

### 1.2 拉代码

```bash
git clone https://github.com/yanceylv518/controlTower.git
cd controlTower/deploy/compose
```

### 1.3 生成强随机凭证（一次生成，粘到 .env）

```bash
echo "AGENT_TOKEN=$(openssl rand -hex 32)"
echo "DASHBOARD_TOKEN=$(openssl rand -hex 32)"
echo "PEPPER=$(openssl rand -hex 16)"
echo "MYSQL_PWD=$(openssl rand -hex 16)"
```

把四行输出复制到记事本备用。

### 1.4 填写 .env

```bash
cp .env.example .env
vim .env
```

逐项填写（右侧为示例，`<>` 处替换）：

```ini
MYSQL_ROOT_PASSWORD=<MYSQL_PWD>
MYSQL_PASSWORD=<MYSQL_PWD>                # 若 example 中分了应用账号则两者都填
CT_DATABASE_DSN=ct:<MYSQL_PWD>@tcp(mysql:3306)/control_tower?parseTime=true
CT_PUBLIC_BASE_URL=http://<CT_IP>:8080
CT_AGENT_TOKEN=<AGENT_TOKEN>              # 全局回退 token，Agent 不直接用它
CT_DASHBOARD_TOKEN=<DASHBOARD_TOKEN>      # 脚本/API 用，浏览器不用
CT_AGENT_TOKEN_PEPPER=<PEPPER>            # ⚠ 定了不可再改，改了全部实例 token 作废
CT_ADMIN_USERNAME=admin
CT_ADMIN_INITIAL_PASSWORD=<自定强密码，首登后改>
```

其余项保持 example 默认。**`.env` 权限收紧**：`chmod 600 .env`。

### 1.5 启动

```bash
docker compose up -d --build
```

首次构建约 3~8 分钟（Node 前端 + Go 编译）。完成后：

```bash
docker compose ps
```

**预期**：`mysql` 状态 `healthy`，`server` 状态 `running`。

```bash
docker compose logs server | tail -20
```

**预期**：能看到 `initial admin created; change the password after first login` 和 `listening on 0.0.0.0:8080`；**没有** `apply migration` 错误。

```bash
curl -s http://127.0.0.1:8080/healthz
```

**预期**：返回 ok 状态 JSON。

### 1.6 浏览器首登

1. 本机浏览器开 `http://CT_IP:8080` → **预期**：Control Tower 登录页；
2. `admin` + 初始密码登录 → **预期**：进入总览，顶栏显示 admin；总览显示"尚未创建实例——前往实例管理"引导条（空库正常现象）；
3. **立即改密**：设置页 → 修改密码 → 会被登出 → 用新密码重新登录成功。

**⚠ 检查点**：以上任一步不符，停止，贴日志/截图给 Claude。

---

## 阶段 2：创建实例（Web，~5 分钟）

实例管理页 → 创建实例，共两个：

| instance_id（严格小写） | 名称 |
| --- | --- |
| `inst-hangzhou` | 杭州 new-api |
| `inst-hongkong` | 香港 new-api |

每次创建成功弹出 **Token 对话框——只显示这一次**：点复制，分别存为"杭州 token"、"香港 token"（记事本/密码管理器），点"我已保存"关闭。

**检查点**：实例列表出现两行，均无 Agent（尚未接入），Token 列不存在（不回显是设计）。

---

## 阶段 3：Agent 接入（一台一台，先香港后杭州）

> 每台约 10 分钟。两条路径：**路径 A** = 该机已有 Agent 在跑（升级+接入）；**路径 B** = 该机从未装过 Agent（全新安装）。香港、杭州各自按实际情况选路径。

### 3.0 每台开始前：确认出站连通

在 new-api 服务器上：

```bash
curl -s -o /dev/null -w '%{http_code}' http://CT_IP:8080/healthz
```

**预期**：`200`。若超时：回头检查安全组第 3/4 条规则的来源 IP 是否就是这台机器的**出口公网 IP**（`curl ifconfig.me` 确认，NAT 环境可能与绑定 IP 不同）。

### 3.1 下载发布包（两条路径都要）

```bash
cd /tmp
wget https://github.com/yanceylv518/controlTower/releases/download/v2.0.0-rc8/control-tower-agent-v2.0.0-rc8-linux-amd64.tar.gz
tar xzf control-tower-agent-v2.0.0-rc8-linux-amd64.tar.gz
cd control-tower-agent-v2.0.0-rc8-linux-amd64   # 目录名以解压实际为准 ls 确认
sha256sum -c <(grep agent-v2.0.0-rc8-linux-amd64 SHA256SUMS) 2>/dev/null || echo "校验清单在包外时跳过"
```

### 3.2 路径 A：已有 Agent 的机器（升级 + 双模式）

```bash
# 1. 停服务、备份旧二进制与配置
sudo systemctl stop control-tower-agent
sudo cp /usr/local/bin/control-tower-agent /usr/local/bin/control-tower-agent.bak
sudo cp /etc/control-tower/agent.config /etc/control-tower/agent.config.bak

# 2. 换新二进制
sudo cp control-tower-agent /usr/local/bin/control-tower-agent

# 3. 编辑配置：追加三行，并把旧钉钉变量替换为企业微信 webhook
sudo nano /etc/control-tower/agent.config
```

追加（以香港机为例；杭州机 token 与 instance id 换成杭州的）：

```ini
CT_SERVER_URL=http://<CT_IP>:8080
CT_AGENT_TOKEN=<香港 token>
CT_INSTANCE_ID=inst-hongkong
CT_WECOM_WEBHOOK_URL=https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=<企业微信机器人Key>
```

**⚠ 注意**：配置里若已有旧的 `CT_INSTANCE_ID`（如 `inst-prod-01`），**必须改成新实例 id**——token 与实例不匹配会被网关 403 拒绝。

**可选第四行（本机 Nginx 已配 timed 日志的机器）**——启用延时分诊采集：

```ini
CT_NGINX_ACCESS_LOG=/var/log/nginx/newapi-timing.log
```

并给 Agent 运行账号日志读权限（二选一）：`sudo setfacl -m u:ct-agent:r /var/log/nginx/newapi-timing.log` 或 `sudo usermod -aG adm ct-agent`。留空/缺文件都不会报错（模块自动禁用/静默重试）。

```bash
# 4. 启动并观察
sudo systemctl start control-tower-agent
journalctl -u control-tower-agent -f
```

**预期**（一分钟内依次出现）：版本行含 `v2.0.0-rc8`；每 30 秒一条 `alert pass` 审计日志；**无** 401/403/connection refused。Ctrl+C 退出观察。

### 3.3 路径 B：全新安装的机器

先按 v1.0 流程准备：该机 MySQL 建只读账号（`GRANT SELECT ON newapi.logs`、可选 `channels`），并创建企业微信群机器人。然后：

```bash
cp agent.standalone.config.example agent.config
nano agent.config    # 填 DSN、企业微信 webhook，并加上 3.2 的三行（token/instance 用本机对应值）
sudo ./install-agent.sh --binary ./control-tower-agent --config ./agent.config
journalctl -u control-tower-agent -f   # 预期同 3.2
```

### 3.4 每台接入后：Web 三查（~2 分钟）

1. 实例管理页：该实例 Agent **在线**、版本 `v2.0.0-rc8`、最后心跳在滚动；
2. 总览页顶栏切到该实例：1~2 分钟后 KPI/趋势出现数据；
3. 系统状态页：该实例的系统指标/健康检查/容器状态有记录。

**全部通过才做下一台。** 香港完成 → 杭州重复 3.0~3.4。

### 3.5 回滚预案（任一台异常时，30 秒）

```bash
sudo systemctl stop control-tower-agent
sudo cp /usr/local/bin/control-tower-agent.bak /usr/local/bin/control-tower-agent   # 路径A才需要
sudo cp /etc/control-tower/agent.config.bak /etc/control-tower/agent.config
sudo systemctl start control-tower-agent
```

即刻回到升级前状态。然后检查日志定位异常。

---

## 阶段 4：双链路验收 + 观察期

### 4.1 当天验收（~15 分钟）

1. **企业微信链路**：直接探测 webhook（不动 Agent 配置）：`curl -sS -X POST '<企业微信webhook>' -H 'Content-Type: application/json' -d '{"msgtype":"text","text":{"content":"[告警] Control Tower 链路测试"}}'`。**预期**：返回 `"errcode":0` 且群里收到消息；再 `journalctl -u control-tower-agent --since -5m | grep "alert pass"` 确认告警循环在跑；
2. **看板巡检**：两实例切换看总览/客户/渠道/模型/用量各页，数据归属正确不串；配了 nginx timing 的机器再看「延时分诊」页（/latency）有分钟桶数据；
3. **磁盘基线**：CT 服务器 `df -h` 记录当前占用（观察期对比用）。

### 4.2 观察期（3~7 天，每天 2 分钟）

- [ ] 两实例在线、心跳正常（实例管理页）
- [ ] 杭州→上海、香港→上海上报无持续报错（各机 `journalctl -u control-tower-agent --since today | grep -ci error` 接近 0）
- [ ] 企业微信告警质量正常（无异常静默/刷屏）
- [ ] CT 服务器 `docker compose logs --since 24h server | grep -ci error` 接近 0；磁盘增长符合预期（保留清理生效）
- [ ] （自动开始，无需配置）调权影子数据在积累：`「延时分诊」旁不用管，观察期结束时查一次 GET /api/dashboard/tuning/report?instance_id=<id>&days=7 有建议流水即可`

### 4.3 收尾

观察期通过 → 告知 Claude 结论 → Claude 写迭代记录 v2.0 章节 → 打正式 tag `v2.0.0` → **v2.0 发布完成**。之后按需：加域名 + Caddy HTTPS、`.env` 里的可选 cron 备份。

---

## 故障速查

| 现象 | 处置 |
| --- | --- |
| Agent 日志 `401 unauthorized` | token 粘贴错/实例被停用；重新核对 token（丢了就 Web 轮换重发） |
| Agent 日志 `403 instance_mismatch` | `CT_INSTANCE_ID` 与 token 所属实例不一致，改配置重启 |
| Agent `connection refused/timeout` | 安全组来源 IP 与该机出口 IP 不符（`curl ifconfig.me` 核对）；或 CT 服务器容器挂了（`docker compose ps`） |
| Web 打不开 | 你的 IP 变了 → 控制台改安全组 22/8080 两条规则 |
| 面板无数据但 Agent 在线 | 等 1~2 分钟聚合；确认实例筛选选对；看 server 日志有无 report 错误 |
| MySQL 容器不 healthy | `docker compose logs mysql`；常见为磁盘满或密码改动未重建卷 |
