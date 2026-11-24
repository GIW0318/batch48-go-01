# batch48-go-game-c-01

## 题面

直播和点播的转码集群最近频繁报花屏：观众看到的画面下半块被切掉，同一帧的分片有的先到有的后到，重组程序只收到一个 marker 包就把残缺的一帧交给解码器；还有一路流跨过序号回绕点之后，后面的包全被当成过期包丢掉，画面直接卡死。另一个机房里有人用同一个时间戳灌进上百个分片，缓冲一路涨到几十兆，进程最后被系统杀掉；运维重启进程之后缓冲全部丢失，观众端又要等下一次关键帧才能恢复画面。请修 gsb/batch48-go-game-c-01 模块里的 rtp_video_frame_assembler.go，类型 FrameAssembler，包名 task，只允许使用标准库。

接口保持：NewFrameAssembler(FrameLimits) *FrameAssembler、Push(RTPPacket) error、PollFrame() (*VideoFrame, bool)、Flush(uint32) int、SetLimits(FrameLimits) error、Pending(uint32) int、Snapshot() ([]byte, error)、Restore([]byte) error、Stats() AssemblerStats、Reset() error。RTPPacket 含 SSRC、Seq、Timestamp、Marker、FragIndex、FragCount、Payload、Keyframe；VideoFrame 含 SSRC、Timestamp、LastSeq、Keyframe、Fragments、Payload；FrameLimits 含 MaxPackets、MaxFrameSpan、MaxFragments、ReorderWindow；AssemblerStats 含 Pushed、Rejected、Duplicates、Reordered、Late、Completed、Dropped、MissingDrops、Malicious、Buffered。

需要修的行为：

一、Push 先做入口校验：Payload 为空、FragCount 小于 1、FragIndex 越界一律返回 ErrMalformedPacket，不进入缓冲，Rejected 加一；通过校验的包计入 Pushed。

二、同一 SSRC 内同一分片重复到达只记 Duplicates，不覆盖已收内容，也不重复交付。

三、乱序窗口内的包按扩展序号落位，补回缺失分片后整帧才算齐；Reordered 记录落位时已有更大序号的包先到的次数。

四、一帧所有分片到齐且收到 Marker 才算收尾完成，Completed 加一；缺 Marker 时停在缓冲里等。

五、帧内缺分片而更新的时间戳已经到达时，整帧丢弃，MissingDrops 加一；半帧交给解码器会直接花屏，任何情况下都不得把半帧交给 PollFrame。

六、丢帧之后同一 SSRC 恢复交付要等下一个关键帧，中间的非关键帧继续丢弃并计入 Dropped。

七、已交付帧的迟到分片记 Late，不允许把同一时间戳再拼出一帧。

八、缓冲同时受 MaxPackets 与 MaxFrameSpan 两条上限约束，超限按最旧时间戳整帧淘汰，Dropped 加一，缓冲不得无限增长。

九、PollFrame 按时间戳升序交付完整帧，时间戳相同时按 SSRC 升序，交付后该帧从缓冲移除，残缺的旧帧不能阻塞后面的完整帧；Flush 丢弃该 SSRC 全部未完成缓冲并按包数返回，其他流不受影响。

十、SetLimits 要求 MaxPackets、MaxFrameSpan、MaxFragments、ReorderWindow 全部为正，否则返回 ErrLimitsRejected 并保留旧限制；下调上限只影响此后进入缓冲的包。

十一、乱序窗口跨过序号回绕点时必须按扩展序号比较新旧，直接用 16 位大小关系会把新包当旧包丢掉。

十二、单帧分片数超过 MaxFragments 视为恶意分片，整帧丢弃并计入 Malicious，不允许为它扩容缓冲。

并发与原子性：同一 SSRC 的并发 Push 必须串行，不得丢包、重复或重复计数；不同 SSRC 之间可以并行，PollFrame 与 Flush 不能看到半个帧。快照恢复：Snapshot 保存各 SSRC 缓冲、交付水位与统计，Restore 坏数据返回 ErrSnapshotCorrupt 且不改动内存状态。验收：go test ./... 的 15 条用例全过，不得改测试、跳过或按固定输入写死；并发与重启自行验证；只做本地实现。

## 模块与运行

- 模块路径：`gsb/batch48-go-game-c-01`
- 包名：`task`
- 实现文件：`rtp_video_frame_assembler.go`
- 测试目录：`tests/`
- 运行全部用例：`go test ./...`
