# 应用层消息分片的交错收集与空洞管理

## 题面


分片收集器是防分片洪泛的边界；攻击者注入重复片、乱序片或超大编号，就能耗尽内存或卡死交付。值班同事反馈大文件分发有三类怪事：一条消息的第 2 片比第 0 片早到十分钟，收集端一直等第 0 片，后面编号更大的消息全堵在队列里交付不出去；同一片被网络复制了一份，内容一模一样却被判成冲突，整条消息作废；内存告警那天，未完成消息只进不出，最后把进程撑死，被淘汰消息的迟到分片又把同号新消息的进度带错。落点 gsb/batch48-go-safety-a-24 的 segment_hole_collector.go，类型 HoleCollector，签名保持：NewHoleCollector(lim Limits) *HoleCollector、Add(f Fragment) (Message, bool, error)、Close(messageID uint64) error、Sweep(now time.Time) []uint64、Pending() []PendingInfo、Poll() (Message, bool)、PendingCount() int、Evicted() []uint64、SkippedIDs() []uint64、Snapshot() ([]byte, error)、Restore(payload []byte) error。Fragment 带 MessageID、SegmentIndex、SegmentCount、Payload 与 Arrived。

要修：分片合法性与消息声明上限没有校验，段数为零、序号越界、空载荷、段数超过单消息上限都能进表；多消息交错时完成顺序不看 messageID，后到的高编号消息先交付，低编号消息永远排在后面；同一分片重复且内容一致被当成冲突，或者被重复计入已收片数；内容不一致时只丢掉该分片，留下半条脏消息继续参与完成判定；后续分片的 SegmentCount 变化时把整条消息清零，已收进度全丢；全局未完成消息数、占用字节、单消息段数没有硬上限，超限既不淘汰也不报错；淘汰只看消息条数不看字节，一条大消息把一堆小消息挤掉；消息被淘汰或关闭后，迟到的分片把旧进度带进同号新消息；Sweep 不按空闲时间清理，也不按头部空洞的等待上限跳过；头部空洞超时跳过时没有记录被跳过的 messageID，下一轮又对同一个空洞重新计时；快照不保存每消息已收位图、首末活动时间与淘汰顺序，恢复后消息重新排队；恢复失败时把现有未完成消息改坏一半；Add 并发时进度更新与完成判定不原子，同一分片能进两次。

硬约束：Add、Close、Sweep、Poll、Snapshot、Restore 在同一把锁下串行，位图更新与完成判定必须原子，多个连接协程同时投递不能重复计片或丢片；未完成消息数、总字节数、单消息段数三项上限硬性生效，超限按最久未活动淘汰而不是整批拒绝；同一分片重复且内容一致按幂等处理，内容不一致整条消息作废；完成消息按 messageID 递增交付，低编号未完成消息不能被更高编号绕过，只有关闭或头部空洞超时跳过才允许跨过；快照要保存每消息的已收位图、首末活动时间与淘汰顺序，恢复要么整体生效要么完全不生效，版本不符或内容残缺一律拒绝并保留旧状态。

验收：go test ./... 15 条全过，不得改测试、跳过或按固定输入写死；并发与重启恢复自行验证；只做本地实现。以上现象均在真实生产流量下复现。
## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-a-24`
- 实现文件：`segment_hole_collector.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`HoleCollector`、`Fragment`、`Message`、`PendingInfo`、`Limits`
