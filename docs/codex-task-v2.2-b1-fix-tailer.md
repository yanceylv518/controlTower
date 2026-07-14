# Codex 任务：v2.2-B1-fix——tailer 数据正确性返工（3 项）

v2.2-B1 验收结论：架构、Server、Web、失效安全全部通过；**tailer 有 3 个数据正确性缺陷需返工**。只改 `agent/internal/nginxtiming/tailer.go` 与 `aggregator.go` 及其测试，其他文件零改动。

**文末自查清单粘贴进 commit message。**

## 缺陷 1（P1，已实证）：部分行被当完整行解析

`reader.ReadString('\n')` 在 EOF 时返回**无换行符的残行**，当前代码 `len(line) > 0` 就解析入桶。nginx 写行途中被读到时：残行 `... status=200 rt=12`（原值 rt=12.345 被截断）解析"成功"——RT 错值、upstream 字段全丢、还可能被误判为慢请求归因；残行后半段下轮被当独立行解析失败丢弃。繁忙日志下最坏每秒污染一条。

验收时的复现测试（修复后请把它收进正式测试，改名 `TestTailerPartialLine`）：写入无换行残行 `... status=200 rt=12` → 等 1.5s → 补写 ` uct=0.1 uht=8.5 urt=11 bytes=9\n` → 断言桶里 `UpstreamCount==1 && UHTMax==8.5`（当前实现产出 `upstream_count=0, rt_max=12, slow_transfer_count=1`）。

**修复**：只处理以 `\n` 结尾的完整行；EOF 返回的残行存入 pending 缓冲，下次读到的内容与之拼接后再按行处理。注意：文件轮转/重开时 pending 必须清空（旧文件的残行不能拼到新文件头上）；pending 设上限（如 64KB，超限丢弃并计入 skipped，防止无换行的异常文件撑爆内存）。

## 缺陷 2（P2）：非 EOF 读错误后全量回放历史

读错误（非 EOF）→ `file = nil` 但 `offset` 保持 >0 → 重开时 `offset != 0` 走 `Seek(0, SeekStart)` **从头读整个文件**：历史行重新入桶，产生大量旧分钟桶，上报后把 Server 里已正确的历史桶覆盖成回放的残缺数据。`offset = 1` 这个轮转哨兵 hack 也顺带清理掉。

**修复**：用显式布尔 `reopenAtStart`（轮转/截断时置 true）替代 offset 魔数：首次打开 seek 到末尾；`reopenAtStart` 时从头读并清 pending；**同文件因读错误重开时 seek 回已记录的 offset**（继续读，不回放）。

## 缺陷 3（P2）：分钟边界乱序导致同分钟桶分裂

`Add` 发现新条目不属于当前分钟就关闭当前桶。多 worker 的 nginx 在分钟边界可能交错写出 `10:00:59 / 10:01:00 / 10:00:59`（time_local 秒级粒度）——10:00 的桶被关掉又新建，队列里出现两个 `bucket_at` 相同的桶，Server upsert 后写为准，**后到的小桶把先前的大桶覆盖掉**，该分钟数据大幅缩水。

**修复**：`current` 从单桶改为按分钟索引的小 map；`Add` 落到对应分钟的开放桶（不存在则建）；桶只由 `Flush(now)` 关闭——`bucket_at + 1 分钟 + 宽限期（5 秒）` 之前的桶保持开放，到期才 close+enqueue；map 中开放桶数设上限（如 5，超限强制关最旧），防时间戳异常撑爆。关闭即入队，同一 bucket_at 不会入队两次。

## 验证要求

1. `make test` 全绿；`go vet` 通过。
2. 新增/改造测试：
   - 缺陷 1 的复现测试（上文场景，含"轮转后 pending 清空"分支）；
   - 缺陷 2：模拟同文件重开（可注入读错误或导出小钩子），断言不重复计数（RequestCount 不翻倍）；
   - 缺陷 3：按 `10:00:59 → 10:01:00 → 10:00:59` 顺序 Add，Flush 到期后断言队列里 10:00 只有**一个**桶且 RequestCount==2；
   - 既有测试原样通过（`waitForBuckets` 里的 Flush 需按新宽限期语义调整时间参数）。
3. 手工冒烟：临时文件 + `printf` 分两段写一行（模拟缺陷 1），确认桶数据正确，过程记入交付说明。

## 交付前自查清单（填好粘贴进 commit message）

- [ ] 只处理完整行；pending 缓冲有上限且轮转时清空
- [ ] 同文件重开续读不回放；轮转重开从头读；offset 魔数已移除
- [ ] 同一 bucket_at 只入队一次；开放桶 map 有上限
- [ ] 三个缺陷各有对应测试；既有测试原样通过
- [ ] 改动仅限 nginxtiming 包及其测试
- [ ] 一个 commit：`fix(agent): nginx timing tailer correctness (v2.2-B1-fix)`

## 明确不做

功能新增、契约/Server/Web 改动、性能优化、episode/持久化。
