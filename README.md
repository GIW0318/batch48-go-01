# 题面

播放器上线多语字幕之后，同一时刻出现两条字幕叠在一起，切换语言后旧轨道的字幕还挂在屏上，长句在窄屏被截掉却没有任何提示；拖动进度条回看时字幕整段消失，过一会儿才重新冒出来，客服收到的录屏里字幕轨一直在闪。问题集中在 gsb/batch48-go-game-c-07 的 subtitle_cue_scheduler.go，CueScheduler 按播放时钟维护活跃 cue 集合，在任意时刻给出应显示的条目，并处理层级优先级、显示数量截断与时钟跳转后的重建。

公开接口：NewCueScheduler(limits Limits) (*CueScheduler, error)、AddCue(c Cue) error、RemoveCue(id string) error、Active() []Rendered、SeekTo(atMs int64) error、SetLimits(limits Limits) error、Clock() int64、CueCount() int、Tracks() []string、Snapshot() (SchedulerState, error)、Restore(SchedulerState) error、Stats() CueStats。Limits 里 MaxCues、MaxTracks、MaxVisible、MaxRows、MaxCols 五项都要生效；Rendered 带 cue ID、轨道、层号、截断后的文本与是否截断的标记；CueStats 分别统计添加数、删除数、活跃数量峰值、因显示上限被丢弃的累计条数与文本被截断的累计条数；Tracks 按首次出现的先后返回当前占用中的轨道。

要修的十二处：ID 或轨道为空返回 ErrInvalidCue；结束时刻早于开始时刻、开始时刻为负返回 ErrInvalidRange；层号为负返回 ErrInvalidLayer；重复 ID 返回 ErrDuplicateCue，删除不存在的 ID 返回 ErrUnknownCue；cue 总数超过 MaxCues 返回 ErrCueLimit 且不落库；轨道数超过 MaxTracks 返回 ErrTrackLimit；同一时刻活跃 cue 按层号从大到小输出，层号相同时按添加先后输出；超出 MaxVisible 的活跃项被截断并计入 DroppedTotal；文本可显示字符上限取 MaxRows 乘 MaxCols，超出部分丢弃并把 Truncated 置为真；SeekTo 传入负数返回 ErrInvalidTime，向前向后跳转后 Active 都按新时刻重建而不是沿用旧集合；cue 结束时刻恰好等于当前时刻时仍应显示，但跨过边界的同一帧不能同时输出前后两条；层号相同的重叠 cue 中删掉一条后，其余 cue 的层内次序必须保持原来的添加顺序。同一个 cue ID 删除后可以重新添加，重新添加按新的添加顺序排在末尾；轨道数量只按当前存在的 cue 计算，某轨道最后一条 cue 被删掉后该轨道不再出现在 Tracks 里。活跃集合里每条 cue 只出现一次，同一轨道同一时刻的两条不同 cue 各自独立显示，返回顺序相同的多条 cue 保留各自的轨道与截断标记，互不覆盖。

硬约束：增删与查询会被字幕渲染和语言下载两条协程并发调用，Active 只能看到完整的新集合或完整的旧集合，不允许出现半条 cue 或重复条目；输出次序不得依赖 map 遍历；限额字段非正时 NewCueScheduler 与 SetLimits 返回 ErrLimitRejected，缩小限额时已有 cue 不得被静默删除；快照要带回全部 cue、限额与当前时钟，恢复后 CueCount、Clock 与 Active 和中断前一致。

验收：go test ./... 的 15 条用例必须全绿，测试不许改、不许跳过、不许按固定文本写死；并发增删与时钟跳转自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块

- 模块路径：gsb/batch48-go-game-c-07
- 包名：task
- 源文件：subtitle_cue_scheduler.go
