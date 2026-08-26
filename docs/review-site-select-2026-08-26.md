# 验收记录：刷新保留站点选择（2026-08-26）

范围：e58273f（一处 watcher 守卫）。结论：通过,零缺陷。

SiteSelect 的 immediate watcher 在 loadInstances 完成前对空列表触发,将持久化的站点选择重置为默认——刷新页面即丢站点。守卫改为 filters.loaded 且列表非空才执行;加载后持久化站点确不存在时回退首个的原行为保留;viewer 逻辑不受影响。typecheck+build 绿。rc70 不含,下 tag 收。
