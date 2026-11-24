# 题面

直播切到新的转码集群后，几个频道黑屏，机顶盒日志里 PAT 版本反复跳变，PMT 还没下发节目表就被清空；另一路信号丢包之后整段数据被丢弃，画面卡住十几秒，运维只能整路重启。改动范围是 gsb/batch48-go-game-c-08 的 ts_demux_guard.go，TsDemuxGuard 负责解析 TS 流里的 PSI 表，跟踪 PAT 与 PMT 的版本，维护节目与基本流映射，识别 PID 冲突与加扰标志，并按字节上限缓存不完整分段。

公开接口：NewTsDemuxGuard(opts Options) (*TsDemuxGuard, error)、PushTable(t Table) error、PushES(p Packet) error、Tables() Tables、Stream(pid int) (StreamInfo, bool)、TableVersion(program int) int、BufferedBytes() int、Resync()、SetOptions(opts Options) error、Snapshot() (DemuxState, error)、Restore(DemuxState) error、Stats() DemuxStats。Options 的 SegmentBytes、MaxPrograms、MaxStreams 三项必须为正；表校验和按表类型字节（PAT 为 0x00、PMT 为 0x02）、版本号单字节、节目号大端 16 位、PID 列表长度与各 PID 大端 16 位、流条目数量与每条流的 PID 大端 16 位加类型字节和加扰字节依次拼接后取 IEEE CRC32，与 Table.CRC 比对；StreamInfo 带 PID、所属节目、流类型与加扰标志；Stats 分别累计成功安装表数、重复版本数、校验失败数、PID 冲突数、包数、未引用 PID 丢弃数、加扰丢弃数、缓冲超限数与当前缓冲字节数。

要修的十二处：未知表类型返回 ErrUnknownTable；版本不在 0 到 31 返回 ErrInvalidVersion；校验和不符返回 ErrTableCRC 并整表丢弃，旧映射保持不变；同一节目同一版本重复下发返回 ErrDuplicateVersion；PAT 里两个节目指向同一个 PMT PID 返回 ErrPIDConflict；PAT 已更新而新 PMT 尚未到达时旧 PMT 继续有效，不得因为 PAT 变化就清空节目映射；PMT 指向不存在的节目返回 ErrInvalidProgram；未在 PMT 中登记的 PID 上送数据返回 ErrUnreferencedPID 且不进入缓冲；单 PID 缓冲超过 SegmentBytes 返回 ErrBufferFull 且不改变已有缓冲；加扰流的负载返回 ErrScrambledStream 并计数；连续计数器不连续的包要打标记但仍按序上送，不得因为计数跳变丢弃整段数据；Tables 返回的映射是副本，调用方改它不影响内部表。PAT 与 PMT 的版本号按五位循环使用，装好的版本要能从 Tables 与 TableVersion 读到；同一 PID 可以连续上送多段负载，缓冲按到达顺序拼接；Resync 只清空缓冲与版本记忆，统计计数保持累计不清零。同一节目重复下发新版本时整表替换该节目的流映射，旧版本的流条目随之失效；未登记的 PID 与加扰流都不占用缓冲额度。

硬约束：PushTable 与 PushES 会被多个 PID 的读流协程并发调用，映射表不允许半更新，读到的节目与流映射必须来自同一份完整表；Resync 之后清空所有不完整缓冲，并让同版本的表重新可以安装；Snapshot 与 Restore 要带回版本、节目与流映射、缓冲字节与统计，恢复后 BufferedBytes 与 Stream 查询结果和中断前一致。

验收：go test ./... 的 15 条用例全绿，不许改测试、不许跳过、不许按固定 PID 写死；并发推送与重启恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块

- 模块路径：gsb/batch48-go-game-c-08
- 包名：task
- 源文件：ts_demux_guard.go
