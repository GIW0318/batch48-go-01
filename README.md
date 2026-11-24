# batch48-go-game-c-14

## 题面

点播平台切到分片 MP4 后，播放器在拖动进度条时卡住：日志显示后一个分片的 tfdt 比前一个还小，解码器直接停摆；有用户连着两个分片拿到同一个 moof 序号，缓存层把它们当成同一段丢掉；还有一路 4K 流把单个分片写到了两百多兆，CDN 回源超时；重试链路把同一个样本写了两遍，分片字节数凭空翻倍；运维在推流中途把轨道时间基从 90k 改成 1k，整条流的时间轴全乱。请修 gsb/batch48-go-game-c-14 模块里的 fmp4_muxer.go，类型 Fmp4Muxer，包名 task，只允许使用标准库。

接口保持：NewFmp4Muxer(MuxConfig) (*Fmp4Muxer, error)、AddTrack(TrackConfig) error、WriteSample(Sample) error、FlushFragment() (*Fragment, error)、SetFragmentLimit(bytes int) error、SetTrackTimescale(trackID uint32, timescale uint32) error、PendingBytes() int、Sequence() uint32、BaseDecodeTime(trackID uint32) (int64, error)、TrackCount() int、Snapshot() ([]byte, error)、Restore([]byte) error、Stats() MuxStats。TrackConfig 含 ID、Timescale、Handler；Sample 含 TrackID、DTS、Duration、Size、Keyframe；Fragment 含 Sequence、TrackIDs、Offsets、Bytes、SampleCount、BaseTime；MuxConfig 含 FragmentLimit；MuxStats 含 Samples、Fragments、Duplicates、Bytes、LimitSplits、Rejects。

需要修的行为：

一、分片字节上限为负返回 ErrLimitRejected；上限为零表示不限制。

二、轨道 ID 与时间基必须为正，ID 重复返回 ErrTrackRejected；登记成功后 TrackCount 立即反映。

三、WriteSample 只接受已登记轨道，样本时长与字节数必须为正，否则返回 ErrSampleRejected，且待写字节不变。

四、同一轨道上解码时间、时长、字节数、关键帧标记全部相同的样本重复写入按幂等处理，字节只算一次，Duplicates 加一。

五、样本解码时间小于该轨道上一笔时返回 ErrDtsRegressed，已收样本与待写字节保持原样。

六、FlushFragment 没有样本时返回 ErrEmptyFragment；分片序号从 1 起连续递增，Sequence 报告下一分片的序号。

七、Offsets 是分片内各样本字节偏移的累计值，首项为 0，总和等于 Bytes，SampleCount 与样本条数一致；BaseDecodeTime 给出该轨道在当前分片的基准解码时间。

八、待写字节加上新样本会超过 FragmentLimit 时先落一个分片再写入新样本，LimitSplits 加一，切分点优先落在关键帧。

九、字节上限与关键帧位置冲突时以上限优先提前切分并允许短分片，短分片仍要有合法的样本表与偏移。

十、连续两个分片的 tfdt 允许相同，但前提是后一分片的样本表非空且样本时长为正；零时长分片必须拒绝。

十一、新轨道在分片中间加入时该分片仍按旧轨道集写完整样本表，新轨道从下一分片起才有样本。

十二、轨道时间基在写入首个样本后锁定，SetTrackTimescale 返回 ErrTimescaleLocked；快照保存轨道、待写样本、分片序号与全部计数，坏数据 Restore 返回 ErrMuxerSnapshot 且不改现状。

并发与硬约束：写入与自动切分在同一把锁内完成，快照与写入互斥，快照不能看到半个分片；分片字节上限是硬顶，超限必须切分而不是继续追加；分片序号与各轨 tfdt 只能单调推进。

验收：go test ./... 的 15 条用例全过，不得改测试、跳过或按固定输入写死；分片上限与重启续写自行验证；只做本地实现。

## 模块与运行

- 模块路径：`gsb/batch48-go-game-c-14`
- 包名：`task`
- 实现文件：`fmp4_muxer.go`
- 测试目录：`tests/`

```bash
go test ./...
```
