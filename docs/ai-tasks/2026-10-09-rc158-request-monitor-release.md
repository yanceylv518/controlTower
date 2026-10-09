# rc158 请求监控远程发布

- 版本：v2.0.0-rc158；固定提交 cf585a47c0b6e7786d2726864066f238ece2ab58。
- 已通过 Git 祖先关系确认包含 rc157、rc156、rc155，以及自动禁用状态3、权限局部滚动、测试跟进时间列/排序和本次ALB请求监控；未按版本号推断包含关系。
- 本版新增：ALB接入配置与加密持久化、全站四档请求数/字节趋势、最多5条高量慢渠道、SLS zlib兼容、最新分钟滑块修复及Host/提示精简。
- 未包含本地独立错误统计及其他未提交内容；远程基于固定标签干净检出构建。

## 验证与发布

发布前CI 37887011871因既有调权4个用例仍期望自动禁用状态2失败，本地复现后仅将对应6处断言改为状态3，业务逻辑未改；tuning全包通过。修复提交 cf585a47c 包含功能提交 b8808c5c8。

- [CI 37887353516](https://github.com/yanceylv518/controlTower/actions/runs/37887353516)全部成功，包括后端测试/构建、前端类型/构建。
- [release 37887594581](https://github.com/yanceylv518/controlTower/actions/runs/37887594581)成功，三个安装包、SHA256SUMS与GHCR镜像发布完成。
- [Release下载](https://github.com/yanceylv518/controlTower/releases/tag/v2.0.0-rc158)。
- 下载后三包大小与SHA256通过；ELF amd64/arm64架构、可执行权限、二进制内嵌提交、Agent版本、Shell LF通过。
- Server包前端请求监控/接入/更新分钟标识存在，无Host汇总；全部已提交数据库迁移逐字节一致，包含124，不含未提交097_error_statistics.sql。
- GHCR rc158与latest摘要一致：sha256:a1f9671f4bdc383dd26eaa204eb9c6efa2b36f0eaa537848b2efa62f3027d820。
- 校验产物：local/releases/v2.0.0-rc158/remote/VALIDATION.json；脚本local/request-monitor-rc158/verify_release.py。
- Server包SHA256：b4bc57e29284c8c7249ec4c309df500077d8e8bfb27342228eba99c10029f380。
- Agent amd64：ca52f3228c82bf685a0d0e5a85f8b5529367b646dff76a55f1c3ea7d28fe160f；arm64：6582f98320b90223e580b626aae3bba48b9f5ef7dec39e5db6af1f665e14e12e。

## 升级范围与边界

需要更新Server/Web，正常启动执行124_alb_access_log_config.sql；既有CT_SECRET_KEY必须保持不变以解密已保存ALB凭证。本版没有Agent功能修改，无需仅为本版再次升级Agent。

用户本地截图已显示ALB请求趋势；生产负载、投递延迟和真实渠道覆盖率仍待验收。此次远程打包发布完成，未部署生产或重启线上服务。发布记录后续文档提交不会改变rc158标签的固定代码提交。
