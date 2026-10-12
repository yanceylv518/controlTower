# rc165 常态探针开关与手动渠道状态远程发布

版本 `v2.0.0-rc165` 固定提交 `7f0872c36820510a799e61f3b087ef5cbf2d1380`。用户在完成提交后重新要求远程打包，本轮恢复发布；此前“暂不打包”属于上一阶段记录。

发布前通过 Git 祖先关系逐项确认包含 rc160～rc164、归档修复 `62fc9b36c`、常态探针开关 `0461572db` 和手动状态控制 `8238e2429`。远程 main 与固定提交一致，没有打包本地未提交功能。

- [固定提交 CI](https://github.com/yanceylv518/controlTower/actions/runs/38239662170)：Go 全项目测试/静态检查/构建及 Web 类型检查/构建全部通过。
- [远程打包](https://github.com/yanceylv518/controlTower/actions/runs/38239963879)：全部成功，正式产物已下载核验。
- [发布下载](https://github.com/yanceylv518/controlTower/releases/tag/v2.0.0-rc165)：已发布。

## 相对 rc164 的功能变化

- 常态探针新增启停配置。关闭后停止定时及错误后加速探针，探针不再参与普通调权；真实流量调权及熔断恢复探测保留。旧配置默认开启。
- 调权中心桌面和手机将渠道状态与操作合并。点击状态标签，弹框选择“开启 / 手动关闭”，保存直接执行，没有二次确认。
- 已启用、自动关闭或自动开启中的渠道均可手动关闭；关闭后按渠道持久接管，停止调权、探测与自动恢复。明确开启时保留线上权重和模型模式，权重 0 不额外抬升。
- NewAPI 侧重新开启后，CT 在新快照确认时解除手动接管，与 CT 开启语义一致。旧快照、关闭未确认及在途请求不会误解除；旧自动写入和旧恢复结果受控制版本保护。
- CT 手动关闭/开启操作保留审计，渠道生命线显示对应操作名称。

实现和行为边界见[探针开关](2026-10-12-routine-probe-toggle.md)、[手动状态控制](2026-10-12-manual-channel-status.md)。

## 升级范围与验证

相对 rc164 没有新增迁移、Agent 或 NewAPI 源码变更；需要更新 CT Server/Web，手动状态接口依赖已配置的 CT 直连。本次仍由标准流程生成 Agent 安装包，但本功能不要求升级 Agent。

发布提交保留原有 139 个迁移；本地未跟踪 `097_error_statistics.sql` 不进入发布。包内迁移集合与逐字节内容、二进制提交/架构/权限、Agent 版本、脚本 LF、Web 新旧功能标识、SHA256 和镜像摘要均已核验。

本地精确提交源码已通过六个 Go 包测试/vet、68 项 Web 回归、类型检查/构建；此前隔离 MySQL 状态与并发保护、外部开启回归及桌面/手机示例交互验证通过。

## 交付边界

只进行远程打包发布，不部署或重启生产，不执行生产迁移，不操作真实渠道。真实 NewAPI 环境验收仍待部署后执行。其他本地未提交的渠道生命线刷新/筛选、实验迁移及历史任务等工作保留。

## 正式产物核验结果

- `control-tower-agent-v2.0.0-rc165-linux-amd64.tar.gz`：`7674267e69a92799e1c5cad2ac00e550a2915d9fefd7b4366c434ef959976089`。
- `control-tower-agent-v2.0.0-rc165-linux-arm64.tar.gz`：`462b7b4171aae7d7c3ca656d51d6a7c6b966634ed2ca595a7f1c103fbbe68ec0`。
- `control-tower-server-v2.0.0-rc165-linux-amd64.tar.gz`：`e4dfd2099d6060e6b0129ce3fcbe257d6580ad6fd5156dac8ba78ef1f2c80b79`。

GHCR `ghcr.io/yanceylv518/controltower-server:v2.0.0-rc165` 与 `latest` 摘要一致：`sha256:369d7ef9200eb2fa803ed5a86e8d9b6aef35f5b9684454a45244b0c7579e7a27`；清单 SHA256 与 linux/amd64 平台已核验。

本地附件和完整报告：`local/releases/v2.0.0-rc165/remote/VALIDATION.json`，复核脚本：`local/releases/v2.0.0-rc165/verify_release.py`。发布标签保持固定代码提交，交付记录另提交到 main；本轮未部署。
