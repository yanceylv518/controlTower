# rc163 用户账单峰谷明细与归档计费用量远程发布

版本 v2.0.0-rc163 固定提交 9c4b903dbff680e43ea0fb485482beb34ff2d5a2。
git merge-base 已核验包含 rc162（6cd3b0d85）和归档修复62fc9b36c，未以版本号推断包含关系。仅从标签提交构建，未混入本地未提交工作。

- [发布下载](https://github.com/yanceylv518/controlTower/releases/tag/v2.0.0-rc163)
- [固定提交CI](https://github.com/yanceylv518/controlTower/actions/runs/38044173801)：成功。
- [远程打包](https://github.com/yanceylv518/controlTower/actions/runs/38044333118)：成功。

## 内容

旧用户账单入口退役并兼容跳转；日账单仅在有明确高峰/低谷档位的模型下展示子行，普通模型保持原状，金额沿用历史结算。不含独立峰谷统计页、独立导出按钮或额外Excel工作表。

同时包含归档summary v3计费用量、图像/音频/缓存分档/工具历史证据及统计起始日期配置修复。实际扣费quota不变。

## 产物核验

三个Linux安装包及SHA256SUMS已下载校验：

- Agent amd64：12153e5787f1a4fdb5e0dcc6877d3fc18b33e029ae460e07420d21b4ab562f34
- Agent arm64：2483084aafcc0e2be69fc463d39f883b6193f86064e2449463e96bbd5bfd138e
- Server amd64：852946c06828d058bd36a288b25bdd52b5400f64726fa55b8de77a948ae72efe

核验ELF架构、执行权限、内嵌Git提交、Agent版本及Shell LF。Server完整迁移集合与固定提交逐字节一致，包含133_billing_tier_statistics.sql，不含未跟踪097_error_statistics.sql。Web包含最终tier_name/峰谷行标识，不含独立导出峰谷按钮，并保留此前版本已发布功能标识。

GHCR版本与latest摘要一致：sha256:d226eb14cdfe7aa9ae968e860a98513b40c57631ef9cecbaa88565ad7050b59b。

验证脚本 local/billing-rc163/verify_release.py；本地证据 local/releases/v2.0.0-rc163/remote/VALIDATION.json。

## 升级范围与边界

- Server/Web及迁移133用于日账单峰谷明细。旧账单不会自动补档位；仅在需要时重新生成对应日账单。
- 需要归档计费用量与统计起始日期修复的节点，应同步升级相应架构Agent；源数据不自动删除或改扣费。
- 月快照复制的MySQL专项和浏览器真实点击验收仍保留原交接中的待验收项，远程打包成功不替代这些验证。
- 未部署或重启生产，未触发实际账单覆盖、归档重建或上游合并操作。
- 发布记录提交不会移动标签固定代码。