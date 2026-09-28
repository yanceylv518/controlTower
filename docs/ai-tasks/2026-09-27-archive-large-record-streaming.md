# 大记录归档分块长期修复

- 标识：2026-09-27-archive-large-record-streaming
- 更新时间：2026-09-27 20:10 +08:00
- 状态：已提交推送并发布rc143；尚未合入main，待生产部署验收。
- 目标与验收条件：解决现场历史2026-08-16、ID23496801、189629491字节超过64 MiB限制；完整保留原始日志，不靠继续放大Agent整行限制，不跳过问题行；重启/失败可接续，校验和统计均可完成。
- 负责会话、分支和范围：本会话，codex/archive-byte-pagination，工作区local/archive-history-20260924；Agent archivejob包及归档库新增暂存表。期间主线发布rc142，已将工作区快进到bb107ee0后接续修复，保留其live统计和批量汇总。没有修改主目录业务代码，没有提交、推送、打包或部署本修复。

## 结果与关键决策

- 当前页先查字段字节数，普通页仍8 MiB；单行超过8 MiB进入分块状态，单块最多4 MiB，单轮最多2块，按数据库包大小可缩小至64 KiB。读块后超过1秒让出轮次；单条SQL耗时另受上下文限制。
- 新log_archive_large_chunks保存原始二进制片段；LargeCollection/LargeHistory/LargeLive检查点存于原state_json，块写入与偏移、SHA状态同事务。不是第三个源库任务，采集/历史沿用已配置轮换比例；live沿用rc142的独立辅助统计步骤。
- 采集和历史补齐最终在归档库内重组字段，与完整SHA校验、月表写入、回执、日期失效和游标同事务提交。失败不暴露半条月表记录。已有相同行只核验复用，不重复写入。
- 归档核验流式计算与旧格式兼容的SHA；封存/live统计流式解析other，保留原有计费键，不保留无关大文本于解析内存。单维度/选中计费证据1 MiB保护，超出明确报告，不静默截断金额。
- 大记录新增或替换使live版本同事务失效后重建；普通差量与批量汇总保留。处理大行期间持续插入普通日志不会反复重置大行扫描；普通行替换旧大行也不把旧大行完整读回Agent。
- 原月表结构不变，新表由Agent初始化创建，不涉及CT迁移。原始字段仍受MySQL类型及max_allowed_packet限制。目标包不足报archive_target_packet_limit，给出required/configured并保留分块；调整配置并使连接重新建立后继续。不会自动修改生产参数。
- 源库仍只读，沿用源日志不更新及完整保留的前提。额外当前页长度/列描述查询和大行主键SUBSTRING查询会增加数据库工作，未做生产RDS压力承诺。数据库重组仍需完整字段内存，不能把Agent分块描述为数据库全程恒定内存。

## 实际验证

工作区未提交版本，隔离本机MySQL9.7（非生产、非MySQL8），专用33387端口及独立数据目录；临时测试库自动清理。

- go vet ./...、go test ./...通过；一般全量命令未配置DSN，实库证据以下方专项为准。
- CT_ARCHIVE_TEST_DSN配置隔离实例后，新引擎全包185.955s通过。其中约181 MiB实库用例175.67s，覆盖采集、源补齐、归档哈希、封存与live统计、二进制字段、分块重启续传、暂存写失败及最终写入被触发器篡改后的事务回滚、完成后暂存清理。
- 包大小不足实库用例：目标4 MiB时缩小传输块，最后发布明确失败且游标不动，恢复包配置后复用暂存接续。
- 最后补修live并发日期版本处理后，9 MiB大行在40次持续追加下完成live统计，quota47；普通小行替换原大行后重建quota43，专项1.659s通过。
- 最终再跑go vet ./...、go test ./...通过；配置隔离DSN后排除已验证181 MiB长用例的新引擎全包回归9.382s通过，包含上述最后补修。
- JSON流式解析差分模糊测试：10秒282486次通过；覆盖跨块/跨重启、无关超大字符串/键/数字、嵌套计价JSON及非法JSON。
- 主线原有live修正/重放、源channel与channel_id结构、批量统计精确等价及事务回滚用例均通过。
- 本次无Web修改，未重跑Web检查；未生产部署、未执行真实远程日志或RDS性能验收。

## 观察进度

在归档库执行，只返回检查点，不读取日志正文：

```sql
SELECT updated_at,
  state_json->'$.Collection.after_id' AS collection_cursor,
  state_json->'$.LargeCollection.ID' AS collecting_large_id,
  state_json->'$.LargeCollection.Column' AS collecting_column,
  state_json->'$.LargeCollection.Offset' AS collecting_offset,
  state_json->'$.History.Step' AS history_step,
  state_json->'$.LargeHistory.ID' AS history_large_id,
  state_json->'$.LargeHistory.Column' AS history_column,
  state_json->'$.LargeHistory.Offset' AS history_offset,
  state_json->'$.LargeLive.ID' AS live_large_id,
  state_json->'$.LargeLive.Column' AS live_column,
  state_json->'$.LargeLive.Offset' AS live_offset
FROM log_archive_meta WHERE singleton_id=1;
```

分块时业务cursor暂不改变，Column/Offset推进；换字段时Offset可归零，换阶段时可重新从头校验。大记录完成后对应Large指针消失，业务游标才越过该行。不要手工删除暂存块或重置检查点。

## 未验证与下一步

### 20:10 继续复查

发现并修复大行live统计失败隔离缺陷：原LargeLive在解析错误后一直保留，退避期间直接返回，其他日期无法构建。现在明确计费数据错误释放该扫描位置并保留日期错误/60秒退避；瞬时错误保留已提交断点，退避期间允许其他普通日期推进。错误处理重新加载已提交状态，再与日期错误同事务保存，不能把失败时内存里增加的偏移写回。

新增隔离MySQL用例验证非法大JSON不阻塞另一日期、瞬时退避时另一日期可完成且原transfer token保持；与持续追加/替换用例一起1.936s通过。最终go vet ./...、go test ./...通过；配置实库DSN的新引擎回归（排除上一轮已通过的181 MiB长用例）10.179s通过，diff检查通过。本轮未重跑181 MiB长用例、未生产验收、未提交发布。仅修改Agent逻辑与测试；隔离测试实例已关闭。

需要授权提交/发布后升级Agent；已有rc142 Server/Web可继续使用。部署前检查归档库max_allowed_packet与列容量，现场189629491字节的大字段应保留足够余量（测试用512 MiB；不是已修改生产配置）。已有月表同内容可复用，但新写入仍受数据库自身约束。新表需要归档写账号现有CREATE权限。

生产仍需验证远程网络、RDS限制、最终重组耗时、实际源库压力及真实日志计价证据。回退旧Agent前应先完成/清理在途大记录，由受控流程处理，不能直接删除状态或降级覆盖新检查点。

相关：[引擎设计](../archive-new-engine.md)、[前期字节分页修复](2026-09-25-archive-byte-pagination.md)。原先未提交的rc137迭代记录和旧任务补充仍保留，不归为本次发布事实。

## rc143正式发布

用户本次授权提交推送打包，沿用之前明确的远程正式发布方式。提交0418042a9093b64f5cc912aeebc059bcddee096b已推送origin/codex/archive-byte-pagination，附注标签v2.0.0-rc143固定同一提交，远程ref已核对。release工作流36318268142成功，GitHub正式Release（非草稿、非预发布）及GHCR版本/latest镜像步骤完成。未推送main、未部署生产。

三个正式tar.gz及SHA256SUMS下载到主目录release/v2.0.0-rc143；实际校验和、ELF amd64/arm64、执行权限、Agent脚本LF、Agent rc143版本及分块暂存/包限制符号、Server前端及097迁移均检查通过。仅本次修复需要Agent升级，已有rc142 Server/Web可继续使用。MySQL包大小/字段容量仍为部署前提。

- [Release](https://github.com/yanceylv518/controlTower/releases/tag/v2.0.0-rc143)
- [远程构建](https://github.com/yanceylv518/controlTower/actions/runs/36318268142)
