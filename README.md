# batch48-go-game-c-12

## 题面

语音会议服务上线静音抑制后，对端开始抱怨：有人静音半分钟再说话，声音是抖的、时间戳跳了一大截；有人刚说完一句话立刻恢复，播放器报重复时间戳直接丢帧；超过半分钟的静音期间对端收不到任何包，判定对端断流挂断；还有一次运维把采样率从 48k 切到 16k，整条时间轴错位，之后所有包的时间戳都对不上。背景噪声估计也被工单点名：空调噪声一上来就顶到最大值，恢复时爆音。请修 gsb/batch48-go-game-c-12 模块里的 dtx_timeline_guard.go，类型 DtxTimelineGuard，包名 task，只允许使用标准库。

接口保持：NewDtxTimelineGuard(DtxConfig) (*DtxTimelineGuard, error)、PushVoice() (DtxPacket, error)、EnterSilence() error、SilenceFrame() (DtxPacket, bool, error)、LeaveSilence(at uint64) (DtxPacket, error)、ComfortNoise(level int) (DtxPacket, error)、SetSampleRate(rate int) error、FrameDurationMs() int、Timestamp() uint32、Seq() uint32、InSilence() bool、SilenceFrames() int、Snapshot() ([]byte, error)、Restore([]byte) error、Stats() DtxStats。DtxConfig 含 ClockRate、FrameSamples、MaxSilenceMs、MaxNoiseLevel、SeqMode；DtxPacket 含 Seq、Timestamp、Samples、Kind、Level，Kind 取 voice、refresh、comfort；DtxStats 含 Voices、Refreshes、Comfort、Gaps、Aligned、Kept、Resets、Silences。

需要修的行为：

一、配置校验：ClockRate、FrameSamples、MaxSilenceMs 必须为正，MaxNoiseLevel 不得为负，SeqMode 只能是 frame 或 packet，且 FrameSamples 换算出的帧长必须是整数毫秒，否则返回 ErrTimelineConfig。

二、PushVoice 发一个语音帧，时间戳按 FrameSamples 推进，序号加一；静音期间调用返回 ErrTimelineInSilence，语音计数不变。

三、EnterSilence 进入静音并把静音帧计数清零；SilenceFrame 每调用一次推进一个帧间隔，frame 模式下每个间隔占一个序号且 Gaps 不增加，packet 模式下只有真正发包才占序号，其余间隔计入 Gaps。

四、单次静音累计达到 MaxSilenceMs 时，SilenceFrame 必须在边界返回一个 refresh 包并计 Refreshes，序号按策略推进；未到边界的调用返回零值包和 false。

五、静音上限按帧数折算，不足一帧的余量不计入强制刷新判断；静音段本身不产生语音包。

六、ComfortNoise 把 level 限制在 0 到 MaxNoiseLevel 之间，发出 comfort 包并推进时间戳与序号，计数进 Comfort。

七、LeaveSilence 以墙钟位置 at 对齐：漂移达到一帧时把时间戳推进到漂移内的整帧边界并计 Aligned，packet 模式下被跳过的帧计入 Gaps；at 早于当前时间戳返回 ErrTimelineClock。

八、漂移不足一帧时按原时间戳继续并计 Kept，不允许为了让时间戳追上墙钟而强行推进，否则恢复首帧会和上一帧撞号。

九、超过最大静音时长强制发包后，时间戳仍要连续，不能跳过整段静音造成时间轴压缩。

十、采样率变化重置时间线：时间戳、序号、静音状态归零，Resets 加一；新采样率让帧长不再是整数毫秒时返回 ErrTimelineConfig 且不改动配置。

十一、快照保存时间戳、序号、静音状态、噪声估计与全部计数，恢复后继续；坏数据 Restore 返回 ErrTimelineSnapshot 且不改现状。

十二、同一 guard 上的 PushVoice、SilenceFrame 与快照必须串行，时间戳与序号只能单调推进，快照不能看到半个静音段。

并发与硬约束：静音帧推进与发包在同一把锁内完成，快照与推进互斥；静音上限、噪声等级都是硬边界，超限夹紧而不是放大。

验收：go test ./... 的 15 条用例全过，不得改测试、跳过或按固定输入写死；并发推进与重启恢复自行验证；只做本地实现。

## 模块与运行

- 模块路径：`gsb/batch48-go-game-c-12`
- 包名：`task`
- 实现文件：`dtx_timeline_guard.go`
- 测试目录：`tests/`

```bash
go test ./...
```
