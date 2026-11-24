# 题面

录制服务升级后，点播回看的首帧花屏，切片时长忽长忽短，断线补传同一片被传了两遍，留存策略删旧片时又把正在上传的那一片算进去，运维翻上传记录只能看到一堆重复条目。问题在 gsb/batch48-go-game-c-09 的 recording_segment_planner.go，SegmentPlanner 在录制流上规划切片：只在关键帧处切分，控制时长上下限，分配单调序号并登记上传状态，断线后按未完成切片补传。

公开接口：NewSegmentPlanner(limits Limits) (*SegmentPlanner, error)、Push(f Frame) error、Plan() ([]Segment, error)、MarkUploading(key string) error、MarkUploaded(key string) error、Replan(keys []string) ([]Segment, error)、Retention(keep int) ([]string, error)、Segments() []Segment、Pending() []Segment、NextSeq() int、Snapshot() (PlannerState, error)、Restore(PlannerState) error、Stats() PlannerStats。Limits 的 MinDurMs、MaxDurMs、MaxAttempts 与 MaxFrameBytes 必须为正，MaxDurMs 不得小于 MinDurMs，否则构造返回 ErrLimitsNotUsable。Plan 返回自上次调用之后新切出的切片；Segment 带切片键、序号、起止时刻、字节数、是否强制切分与上传状态、重试次数；Segments 按序号升序返回副本，Pending 只返回尚未上传的切片；Retention 按传入的保留条数淘汰最旧的已上传切片，返回被淘汰的切片键并保留从旧到新的顺序；Replan 把列出的切片重新交给上传并累加重试次数；Stats 分别累计入帧数、切出切片数、已上传数、淘汰数、重规划次数与累计推入的总字节数。

要修的十二处：首帧必须是关键帧，否则返回 ErrKeyframeRequired；PTS 不严格递增返回 ErrNonMonotonicPTS；单帧字节数超过 MaxFrameBytes 返回 ErrFrameTooLarge；当前片时长未到 MinDurMs 时遇到关键帧不得切分；达到 MinDurMs 后的关键帧必须切分并把新片起点定在该关键帧；到达 MaxDurMs 仍未遇关键帧时强制切分，强制切分后剩余时长仍超上限时必须连续补切到每片合规；切片序号从 1 开始单调递增，重规划不回收旧号；切片键在同一批次内唯一且稳定，MarkUploaded 与 Replan 都按它定位；MarkUploaded 重复调用视为成功且上传计数只加一次；同一切片已有上传在途时再次 MarkUploading 返回 ErrUploadInProgress；留存淘汰只统计并删除未被引用的已上传切片，正在上传的切片即使最旧也不参与淘汰；补传重试达到 MaxAttempts 的切片转入人工处理，既不参与留存淘汰也不允许被自动删除。切片键由批次与序号拼成，重启后重新算出的键必须与中断前一致；切片刚切出时是待上传，MarkUploading 之后是上传中，MarkUploaded 之后是已上传；已上传的切片再次写入只刷新上传时间，不会产生第二个台账条目。

硬约束：Push 与 Plan 会被采集与调度两条协程并发调用，已切出的切片不得因并发重排顺序或重复出队；Snapshot 与 Restore 之间序号、未切完的帧缓冲与上传台账要连续，恢复后下一片序号与中断前一致；留存条数与重试次数都有硬上限，超出只淘汰最旧的合规切片，不得整表清空。

验收：go test ./... 的 15 条用例必须全绿，测试不许改、不许跳过、不许按固定 PTS 写死；并发切分与重启续跑自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块

- 模块路径：gsb/batch48-go-game-c-09
- 包名：task
- 源文件：recording_segment_planner.go
