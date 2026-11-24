# batch48-go-game-c-02

## 题面

一场线上发布会的导播台被投诉：主持人的嘴型比声音慢半拍，字幕对不上；切到连麦嘉宾后画面和声音又反过来，音频先响、口型提前。切源之后十分钟内漂移越来越大，导播手动点同步也没用，因为旧的估计样本还挂在里面。更麻烦的是切到第二路信号时播放器直接黑屏不动，值班同事说它在等"稳定下来再播"，可现场不能停。请修 gsb/batch48-go-game-c-02 模块里的 av_sync_guard.go，类型 AvSyncGuard，包名 task，只允许使用标准库。

接口保持：NewAvSyncGuard(Thresholds) *AvSyncGuard、SetMasterClock(ClockSource) error、MasterClock() ClockSource、PushVideo(VideoFrame) error、PushAudio(AudioBlock) error、Decide() (SyncDecision, error)、SetThresholds(Thresholds) error、ResetSource() error、DriftEstimate() int64、QueueLen() (int, int)、PendingPTS() []int64、Snapshot() ([]byte, error)、Restore([]byte) error、Stats() SyncStats。VideoFrame 含 PTS、Keyframe、Seq；AudioBlock 含 PTS、Duration、Position；Thresholds 含 SyncWindow、MaxStepPerCall、SmoothingSamples、AudioQuantum、MaxQueue；SyncDecision 含 Action、Frame、RepeatCount、ResampleRatio、Drift、DriftAfter；SyncStats 含 Decides、Drops、Repeats、Resamples、KeyframeDrops、PushedVideo、PushedAudio、Rejected、ResetSamples、InSync。

需要修的行为：

一、SetMasterClock 只接受 audio、video、external 三种取值，其他返回 ErrClockUnsupported 且不改当前主时钟；主时钟未设置时 Decide 返回 ErrNoMasterClock。

二、PushVideo 要求 PTS 严格递增，重复或倒退返回 ErrVideoOrder 且不入队；ResetSource 之后允许从任意 PTS 重新起步。

三、PushAudio 要求 Duration 为正，否则返回 ErrAudioBlock；Position 为负表示设备未上报，按 PTS 处理。

四、漂移原始值等于音频播放位置减视频呈现时刻，Decide 每次追加一个样本，估计值取最近 SmoothingSamples 个样本的均值。

五、平滑漂移在 SyncWindow 内输出 keep，不消耗视频队列，DriftAfter 等于当前估计值。

六、音频为主时钟且视频落后时输出 drop_video，弹出最旧的一帧；视频超前时输出 repeat_video，重复一帧而不推进时间线。

七、视频作主时钟或外部时钟时改为调整音频，输出 resample_audio 并给出比例；比例等于一加补偿步长除以一百万，音频落后时大于一，音频超前时小于一。

八、音频设备上报的播放位置带有 AudioQuantum 量化误差，估计漂移时必须先扣除该误差，不能把量化噪声当成真实漂移连续丢视频帧。

九、单次补偿步长不超过 MaxStepPerCall，DriftAfter 是补偿后剩余漂移，连续调用逐步收敛而不是一次跳完。

十、连续多次决策仍超出窗口时仍要按最接近的时刻继续输出，不能停播等待人工干预。

十一、视频队列同时存在关键帧与普通帧时优先丢普通帧，关键帧被丢弃必须计入 KeyframeDrops 并随统计上报。

十二、视频队列达到 MaxQueue 时丢最旧一帧再收新帧；SetThresholds 校验 SyncWindow 非负、MaxStepPerCall 为正、SmoothingSamples 至少为一，违规返回 ErrThresholdsInvalid 且保留旧阈值。

并发与原子性：PushVideo、PushAudio 与 Decide 并发时只能看到完整队列，同一帧不会被两次决策消费。幂等与顺序：相同帧序列重复喂入得到相同动作，Decide 不因重复调用累加 Drops。切源与重启：ResetSource 清空平滑样本、补偿偏移与队列，DriftEstimate 归零，ResetSamples 加一；Snapshot 保存主时钟、阈值、样本、队列与统计，Restore 坏数据返回 ErrStateCorrupt 且不改动内存状态。验收：go test ./... 的 15 条用例全过，不得改测试、跳过或按固定输入写死；并发与重启自行验证；只做本地实现。

## 模块与运行

- 模块路径：`gsb/batch48-go-game-c-02`
- 包名：`task`
- 实现文件：`av_sync_guard.go`
- 测试目录：`tests/`
- 运行全部用例：`go test ./...`
