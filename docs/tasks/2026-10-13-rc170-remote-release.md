# rc170 生命线当前状态超时修复远程发布

- 日期：2026-10-13（Asia/Shanghai）
- 授权：提交推送并远程打包。
- 状态：代码推送及远程发布完成，正式成品核验通过；未部署。
- 标签：`v2.0.0-rc170`，固定完整提交 `49af43600b21e858dbd8b469ccf0f1937ea7a16b`。

## 内容与范围

生命线当前状态加载超时修复已发布，标签固定49af4360，祖先核验包含rc160～rc169及此前刷新/异常筛选/手动关闭隐藏修复。当前恢复查询改为活动任务与在途请求候选集合，迁移139增加pending查询索引；当前状态全链路15秒查询时限、前端20秒截止、失败不伪装0个渠道。固定源码29项Web回归、远程CI/release、三正式附件与GHCR版本/latest核验通过。需Server/Web及迁移139配套更新，无Agent业务修改；未部署。

原rc169查询在隔离库20,000条完成命令和过期pending记录下超过15秒，同批数据改写并加索引后候选查询约67毫秒；这是合成复现，不是生产数据量或上线延迟承诺。生产仅只读核对当前接口约15秒返回503；未修改渠道、策略或探针任务。实现与复现详见[交接](2026-10-13-lifeline-current-state-design.md#rc169后续当前状态加载超时2026-10-13)。

只提交本次11个文件/选定进度条目，其他本地请求监控等工作保留。修复不改变探针执行、熔断、恢复与自动开启规则。

## 验证

实现阶段Go全项目vet/test、六项隔离MySQL集成（含规模和连接池超时）、29项Web回归、typecheck/build通过。浏览器合成场景确认超时停止加载、重试成功及刷新失败保留快照。本轮固定提交导出源码29项Web回归通过；开始因导出目录未配置desktop依赖无法加载测试，接入已有依赖后重新执行全部通过。远程干净提交CI执行Go vet/test/build及Web typecheck/build通过，既有大包提示保留。

- [CI 38325511700](https://github.com/yanceylv518/controlTower/actions/runs/38325511700)：成功。
- [release 38325742897](https://github.com/yanceylv518/controlTower/actions/runs/38325742897)：成功。
- [发布与下载](https://github.com/yanceylv518/controlTower/releases/tag/v2.0.0-rc170)。

## 产物与升级

三个Linux安装包SHA256、ELF架构/执行位、完整内嵌提交、vcs.modified=false、Agent版本和脚本LF核验通过。141个迁移与标签源码逐字节一致，包含新增139；Web包含本次超时/数量未读取提示及前序生命线功能标识。

- `control-tower-agent-v2.0.0-rc170-linux-amd64.tar.gz`：`22d89623ecddfd1a416d4bf7bae3d11a093bb2552bc8f48ef5730eb1d684dafa`。
- `control-tower-agent-v2.0.0-rc170-linux-arm64.tar.gz`：`70dc53766558a50bf6039dbd3b2657d5d7e8e938c5d69eb8e73ee54c0b6e6f29`。
- `control-tower-server-v2.0.0-rc170-linux-amd64.tar.gz`：`26c3afadfd11f2b9609a6269d37616cb8b7ee1d57e451e7d77bdff4604db3c12`。

GHCR版本/latest摘要一致：`sha256:b24fcbd2c3fb07eda6a69f40dac95a968f2ed0233fa272dc7bd2fe1f5881584e`，linux/amd64；未启动容器。需更新Server/Web并执行迁移139，新索引在生产数据量上的创建耗时待部署验证。无Agent业务变化。未生产部署、重启或执行生产迁移；上线后仍需复测pinducloud-cn响应。

核验记录：`local/releases/v2.0.0-rc170/remote/VALIDATION.json`。发布事实另提交main，标签不移动。
