# 验收记录：newapi 严格渠道更新适配（2026-08-10）

范围：6c915c4（codex 交付,channelcontrol 客户端）。验收环境：Linux,vet+全量三树测试、直连真库集成（strict 假件）、desktop build 全绿。

## 结论：通过;定性为 P1 级修复;一处验收修正（strict 假件,自领）

## 源码实证（scratchpad/newapi-src,逐字核对）

- `UpdateChannel`（PUT /api/channel/）入口即拒:`if _, ok := requestData["status"]; ok → MsgInvalidParams`——**通用更新带 status 字段必被拒**,codex 注释与源码行为一字不差;
- 状态专用端点 `POST /api/channel/:id/status`,body `{"status": int}`（ChannelStatusRequest+isManageableChannelStatus 校验）——codex 的分离实现与端点/方法/形态全部吻合;
- 其余 GET 回读字段由服务端 `clearChannelReadOnlyFields` 自行清理,不拒,原样转发安全。

## 严重性认定

修复前,**GET→原样 PUT 的转发链路在当前 newapi 版本上必然失败**（"Invalid parameters"）——即:rc44 的直连预检、写权重、探针恢复写全部不可用;**agent rc22 的同源转发逻辑同样是坏的**,"回落命令路径可用"的 B1 假设对当前 newapi 不成立。生产未暴露仅因调权从未离开 observe。部署语义因此改变：

- **直连要可用必须含本修复——rc44 作废该用途,需 rc45**;
- **回落站点的写路径需 agent 升级到含本修复的版本才真正可用**;在此之前直连配置是事实必需而非可选优化。

## 验收修正（自领教训）

我的直连真库集成测试假 newapi **不拒 status**——修复前的坏客户端在它面前全绿,182f3b7"fixture 与实现同错"的教训在我自己写的假件上重演。已补 strict 行为（PUT 带 status 回 Invalid parameters,注明源码依据）,修复后客户端在 strict 假件下全链路通过,坏客户端从此无法通过集成测试。**规矩追加:写外部系统假件时,拒绝路径与接受路径同样要按源码核对。**

## 核验

- 三个新测试:通用更新体不含 status/无变更更新在 strict 服务端通过/status 走专用端点且通用体不带;
- CT 调权链路从不设置 Status（引擎只写 weight/priority,熔断不动 status）——分离端点仅服务 ops 渠道启停命令,该端点当前源码实存;
- 对老版本 newapi 兼容性:删 status 对不拒绝的版本同样无害（不改 status 时本就无需传）。

## 部署

无迁移。**rc45 必须重打**（rc44 的直连对真实 newapi 不可用）;agent 若要保留回落写能力需同步出含本修复的 agent 版本。
