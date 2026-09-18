# 本地改动整合 main 与打包

- 目标：按用户明确授权将全部尚未交付本地代码整合到 origin/main，生成项目标准发布包。
- 状态：已提交推送origin/main，rc122打包发布成功，三个包下载校验通过；未部署。更新时间：2026-09-18。
- 任务 ID：01a0b317-381b-7bd3-bdca-04d98b2e6d0f。
- 工作目录：E:/projects/controlTower/.tmp/main-release-20260918；分支：codex/main-release-20260918，基于 origin/main。
- 范围：各工作区新增且未交付的业务代码、相关回归和文档，提交推送与标准Linux包。
- 范围外：本地缓存、预览配置/凭据、未定稿设计、生产部署。保留各源目录，不清理既有混合改动。
- 验收：旧改动去重，合并主题/通知/上游及优先级自动纠正，相关检查通过，远程main可核对提交，包和校验和可交付。
- 已确认：远程main为2e51f786f；本地main旧于远程且有已交付残留；主题目录有新增通知/上游/图标/对比度修复，优先级目录另有自动纠正未提交。
- 整合：核对16个既有工作区；主目录93个候选业务文件中64个无变化、27个历史已交付、1个三方合并无新增；BillingRecords保留主线更完整的上游账单分组。旧direct-TTFT的4个重叠文件保留后续OTPS替代及新版UI，未回退算法。其他旧工作区无独有业务差异。
- 新增交付：主题目录22项业务文件、优先级目录21项业务文件（含新增文件）；调权页面手工合并保存优先级/熔断可编辑语义与最新紧凑布局，同时修正帮助文案；通知、图标、上游重设计与按钮对比度一并纳入。
- 验证：226项前端测试、vue-tsc/Vite生产构建、Go test ./...、Go vet ./...通过；diff检查通过。首次前端优先级测试在冲突未解时失败，完成合并后全量重跑226/226通过。未配置MySQL DSN，本轮不声明实库验证。构建保留既有大包提示。
- 版本：v2.0.0-rc122已发布。标准v*标签工作流生成Linux Agent amd64/arm64、Server amd64、SHA256SUMS和GHCR镜像，未部署。
- 交付：业务提交bc743c0e7283bee0635c145bba8f5438f8ddb49b（48文件）已快进推送origin/main，标签v2.0.0-rc122解引用同一提交。工作流 https://github.com/yanceylv518/controlTower/actions/runs/35315522341 最终success；发布页 https://github.com/yanceylv518/controlTower/releases/tag/v2.0.0-rc122 。后续交付文档提交不改变发布代码。
- 本地范围：原主工作区仍是旧main及其既有混合改动，未强制重置或清理；本次以隔离目录向远程main完成整合。所有源工作区保留，残留差异不代表尚未交付同一代码。
- 产物：三个tar.gz及SHA256SUMS已下载至E:/projects/controlTower/dist/releases/v2.0.0-rc122；逐包大小和SHA256与发布清单匹配，Agent包各10项含双程序及双安装脚本，Server包94项含程序、前端和083迁移。
- 校验：Agent amd64 d56f52675ae95b82c2dd85c6e81437e5694133cb69efda76db0dac59043fb7b6；Agent arm64 92ac1c00880371748cf925fed708a2c3a36ef1727de9804fe6b018986acf7488；Server amd64 1b0b2bd390343b75eb1285419e2a761f17fef204dd4eeaf6ef35c0cacba056cb。
- 下一步：按需部署Server/前端并执行现场验收；本次未重启或升级任何生产服务。
