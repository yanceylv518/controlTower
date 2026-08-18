# 验收记录：账单时区收尾+用户请求明细页（2026-08-18）

范围：e13f558+b8bee9d（codex 交付）。验收环境：Linux,vet+server 全量测试、vue-tsc、desktop build 全绿。

## 结论：两笔均通过,零缺陷

## e13f558 业务时区收尾

配价生效日期/回填区间/导入默认日期/日志导出月份/模型清单等残余 `time.Local` 解析全部改 billing.BusinessLocation（Asia/Shanghai）——**闭环 rc27 记档的"time.Local 残留四处（容器 UTC 偏 8h）"P3 欠账**;billing 处理器已 grep 零残留。测试断言时区偏移=+8h（配价/回填/日切日期三处),billingSyncDay 钉 UTC 16:30→沪 %2B08:00 次日零点。

## b8bee9d 用户账单请求明细（request_id 查询）

新端点 GET /billing/requests:用户×区间的逐请求清单（log_id/request_id/模型/四类 token/quota）,复用直连只读通道与昨日的 id 主键游标（after_id 键集翻页,page_size≤200,+1 探测 has_more）;**读闸完整**:billingJobForRead 绑完成任务（409 语义与账单页一致）、非 admin 角色站点+用户双重 scope 校验（纵深,auth 层白名单本就挡 viewer 于 billing 之外）;时间格式化走业务时区。前端账单抽屉加载更多式列表。测试钉分页边界与游标透传。

## 部署

无迁移。**用户确认生产服务器为上海时区——time.Local 本就等于 BusinessLocation,本修复对生产为纯 no-op,存量配价无偏差,无需抽查或重生成**;价值=正确性不再依赖环境时区配置（换机/容器忘设 TZ 也不歪）,属无害加固。时区笔为 codex 搭车交付（用户仅令做 request_id 功能）,方向正确（rc27 记档 P3 的欠账）,用户知情后接受保留——流程记档。
