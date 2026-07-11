# 错误预警 Agent 部署文档

> 本文档内容已并入 `iteration-log.md` 的「v1.0 错误预警版」章节（部署实录、部署问题、已知限制各节）。后续迭代记录统一维护在 `iteration-log.md`，本文档保留作为 v1.0 部署的独立快照，不再更新。

本文档记录 Control Tower 错误预警 Agent 在 Ubuntu new-api 服务器上的部署流程、验证方法、遇到的问题及解决办法。

## 1. 部署范围

本次部署使用 standalone 模式：

- Agent 直接读取 new-api MySQL 的 `logs` 表。
- Agent 每 30 秒增量查询一次。
- Agent 按渠道和客户分别维护最近 10 条请求。
- 最近 10 条中错误数达到 3 条时发送钉钉告警。
- 不需要部署 Control Tower Server。
- 不修改 new-api 代码、路由或请求链路。
- 不读取请求体、响应体、API Key 或 Cookie。

## 2. 确认服务器架构

执行：

```bash
uname -m
```

本次服务器返回 `x86_64`，因此选择：

```text
error-alert-agent-63b31fc-linux-amd64.zip
```

对应关系：

| `uname -m` | 部署包 |
|---|---|
| `x86_64` | `linux-amd64` |
| `aarch64` | `linux-arm64` |

## 3. 创建 MySQL 只读账号

Agent 只需要读取 new-api 的日志表。在 new-api MySQL 中执行：

```sql
CREATE USER 'ct_readonly'@'%' IDENTIFIED BY '设置强密码';

GRANT SELECT ON newapi.logs TO 'ct_readonly'@'%';

FLUSH PRIVILEGES;
```

检查权限：

```sql
SHOW GRANTS FOR 'ct_readonly'@'%';
```

不应授予 `INSERT`、`UPDATE`、`DELETE`、`ALTER` 或 `DROP` 权限。

确认日志表索引：

```sql
SHOW INDEX FROM newapi.logs;
```

重点确认 `logs.id` 存在索引。Agent 使用日志 ID 增量读取，不会每 30 秒扫描整个表。

## 4. 创建钉钉机器人

在钉钉目标群中：

1. 打开群设置。
2. 进入机器人管理。
3. 添加自定义机器人。
4. 安全设置选择“自定义关键词”。
5. 关键词设置为：

```text
告警
```

6. 复制机器人 Webhook 地址。

当前 Agent 使用 Webhook 方式发送文本告警，暂不支持钉钉加签模式。

## 5. 上传和解压部署包

从本地上传：

```powershell
scp "error-alert-agent-63b31fc-linux-amd64.zip" root@SERVER_IP:/tmp/
```

登录服务器：

```bash
ssh root@SERVER_IP
```

Ubuntu 可能没有安装 `unzip`，先安装：

```bash
apt-get update
apt-get install -y unzip
```

解压：

```bash
rm -rf /tmp/control-tower-agent
mkdir -p /tmp/control-tower-agent

unzip /tmp/error-alert-agent-63b31fc-linux-amd64.zip \
  -d /tmp/control-tower-agent

cd /tmp/control-tower-agent
ls -lh
```

正常应包含：

```text
control-tower-agent
install-agent.sh
control-tower-agent.service
agent.standalone.config.example
README.md
SHA256SUMS
```

## 6. 配置 Agent

复制配置模板：

```bash
cp agent.standalone.config.example agent.config
nano agent.config
```

填写：

```ini
CT_AGENT_ID=agent-prod-01
CT_INSTANCE_ID=inst-prod-01
CT_LOG_DSN=ct_readonly:数据库密码@tcp(127.0.0.1:3306)/newapi?parseTime=false&timeout=2s
CT_DATA_DIR=/var/lib/control-tower-agent
CT_DINGTALK_WEBHOOK_URL=https://oapi.dingtalk.com/robot/send?access_token=钉钉Token
CT_ALERT_ERROR_WINDOW=10
CT_ALERT_ERROR_THRESHOLD=3
CT_LOG_POLL_INTERVAL_SECONDS=30
```

说明：

- `CT_LOG_DSN` 同时包含数据库用户名和密码。
- standalone 模式不需要配置 `CT_SERVER_URL`。
- `CT_ALERT_ERROR_WINDOW=10` 表示最近 10 条。
- `CT_ALERT_ERROR_THRESHOLD=3` 表示 3 条错误触发。
- `CT_LOG_POLL_INTERVAL_SECONDS=30` 表示每 30 秒查询一次。
- 不要把真实配置提交到 Git。

## 7. 修复 Linux 换行并安装

由于部署包可能在 Windows 环境生成，Shell 文件可能带有 CRLF 换行。先执行：

```bash
sed -i 's/\\r$//' install-agent.sh
sed -i 's/\\r$//' control-tower-agent.service
chmod +x control-tower-agent install-agent.sh
```

然后安装：

```bash
sudo ./install-agent.sh \
  --binary ./control-tower-agent \
  --config ./agent.config
```

安装脚本会创建 `ct-agent` 用户、安装二进制、创建数据目录、执行 preflight、安装 systemd 服务并启动 Agent。

## 8. 验证安装结果

```bash
sudo systemctl status control-tower-agent
sudo systemctl is-active control-tower-agent
sudo systemctl is-enabled control-tower-agent
sudo journalctl -u control-tower-agent -f
```

正常状态：

```text
Active: active (running)
```

正常 preflight 日志应包含：

```text
preflight pass mysql_ping: connected
preflight pass logs_table: queryable
preflight pass logs_id_index: logs.id index found
```

首次启动时，Agent 从当前最新日志 ID 开始，不扫描历史日志，因此不会因历史错误立即发送告警。

## 9. 本次遇到的问题和解决办法

### 9.1 Ubuntu 没有 unzip

现象：

```text
Command 'unzip' not found
```

解决：

```bash
apt-get update
apt-get install -y unzip
```

### 9.2 Windows 换行符导致 bash 启动失败

现象：

```text
/usr/bin/env: 'bash\\r': No such file or directory
```

原因是 `install-agent.sh` 使用了 Windows CRLF 换行，Ubuntu 识别到多余的 `\\r`。

解决：

```bash
sed -i 's/\\r$//' install-agent.sh
sed -i 's/\\r$//' control-tower-agent.service
```

后续生成 Linux 部署包时，应确保 Shell 脚本和 systemd 文件使用 LF 换行。

### 9.3 配置模板文件名带 example

`agent.standalone.config.example` 是模板，不应直接作为生产配置。正确做法：

```bash
cp agent.standalone.config.example agent.config
nano agent.config
sudo ./install-agent.sh --binary ./control-tower-agent --config ./agent.config
```

### 9.4 Webhook Token 泄露

如果 Webhook 出现在截图、聊天或日志中，应立即在钉钉机器人设置中重新生成 Webhook，然后更新：

```bash
sudo nano /etc/control-tower/agent.config
sudo systemctl restart control-tower-agent
```

不要将真实 Webhook、数据库密码或生产配置提交到 Git。

## 10. 当前告警逻辑限制

当前告警依赖 new-api 将请求结果写入 `logs` 表。

如果请求需要等待 600 秒才最终写入 504，那么告警时间大约是：

```text
600 秒 + 0 到 30 秒
```

因此当前 Agent 无法发现仍在执行中的请求，也无法在请求完成前判断它最终是否会返回 504。

当前方案适合已完成请求的客户和渠道维度告警，不适合请求尚未结束时的实时告警。

## 11. 600 秒超时盲区的解决办法

推荐使用“两层告警”。

### 第一层：网关快速告警

在 Nginx、SLB 或其他反向代理层采集：

- HTTP 状态码
- 504 数量
- 5xx 数量
- 请求耗时
- upstream 响应耗时
- 连接失败数量

建议阈值：

```text
请求耗时超过 60 秒：慢请求告警
请求耗时超过 300 秒：严重慢请求告警
出现 504：立即告警
连续出现 3 个 504：升级告警
```

这层不等待 new-api 写入 `logs`，可以快速发现 600 秒内的请求异常，但通常只能识别实例和接口，未必能准确识别客户和渠道。

### 第二层：Agent 业务维度告警

继续保留当前 Agent：

```text
读取 logs
→ 按客户和渠道维护最近 10 条
→ 3 条错误触发告警
```

这层延迟较大，但可以准确回答哪个客户、哪个渠道持续失败。

### 后续如需提前识别具体客户和渠道

需要在请求开始时产生轻量事件，例如：

```text
request_id
user_id
channel_id
model
started_at
```

可以通过 new-api middleware、网关请求头或本地 Unix Socket/事件文件实现。事件中不需要包含请求体、响应体、API Key 或 Cookie。

## 12. 停止和卸载

停止服务：

```bash
sudo systemctl disable --now control-tower-agent
```

卸载：

```bash
sudo rm -f /etc/systemd/system/control-tower-agent.service
sudo rm -f /usr/local/bin/control-tower-agent
sudo rm -rf /etc/control-tower
sudo rm -rf /var/lib/control-tower-agent
sudo systemctl daemon-reload
```
