# 题面

在线课堂的连麦房间换了回声消除之后，学生端在老师说完话的两三秒里还能听到发闷的尾音，安静段落偶尔冒出细微白噪；两名学员同时抢话时，尾音比升级前更明显，值班同学把录音拉出来逐帧对比，发现尾音都集中在双讲结束后的十几帧里，而安静段落的底噪还会慢慢抬高。问题落在 gsb/batch48-go-game-c-06 的 residual_echo_suppressor.go，EchoSuppressor 要根据参考帧与近端帧的能量把每一帧判成远端单讲、近端单讲、双讲三种状态，再按状态调整抑制增益并在远端单讲期间注入舒适噪声。

公开接口：NewEchoSuppressor(frameSize int) (*EchoSuppressor, error)、PushReference(frame []float64) error、PushNearEnd(frame []float64) error、PopFrame() ([]float64, bool)、SetThresholds(t Thresholds) error、State() string、Gain() float64、ComfortNoiseLevel() float64、Reset()、Snapshot() (EchoState, error)、Restore(EchoState) error、Stats() Stats。帧长在构造时固定，非正帧长直接返回错误；每帧能量取样本平方的均值；三态常量沿用 StateFarEnd、StateNearEnd、StateDoubleTalk；初始状态为近端单讲、增益为 1.0。Stats 要分别累计入帧数、出帧数、三态各自的帧数、舒适噪声帧数与发生过抑制的帧数。

要修的十二处：帧长不符返回 ErrFrameSize，被拒的帧不得推进任何计数；参考是否活跃用 RefEnter 与 RefRelease 双门限，已活跃的参考只按释放门限退出；没有参考帧时按近端单讲处理；状态切换要满足 MinDwell 帧的最短驻留；增益朝目标值移动时每帧不超过 MaxGainStep；三态目标增益分别为 SuppressGain、DoubleTalkGain 与 1.0；双讲结束回到远端单讲时，抑制深度要比进入双讲之前更深，不能把可听回声留在尾音里；远端单讲帧计入 ComfortFrames 并叠入舒适噪声，舒适噪声只叠加在远端单讲帧上，近端单讲与双讲帧不得注入；舒适噪声电平跟随背景估计下调，且不得高于被抑制信号的电平；PopFrame 按入队顺序出帧，队列空时返回 false；阈值非法（能量非正、释放门限不低于进入门限、驻留小于 1、步长不在 0 到 1 之间）返回 ErrInvalidThreshold；快照里的状态名不认识或增益越界时 Restore 返回 ErrBadSnapshot。Reset 要回到近端单讲、增益 1.0 并清空全部统计。

硬约束：输出队列最多 64 帧，满时 PushNearEnd 返回 ErrQueueFull 且不改动已有队列；PushReference、PushNearEnd 与 PopFrame 会被采集与播放两条协程并发调用，取出的每一帧都必须是完整帧，不得丢帧或重复出队；同一段录音重放两遍，出帧序列要逐样本一致，采集侧送帧节奏可以抖动，播放侧按自己的节奏取帧；Snapshot 与 Restore 之间增益、驻留计数、三态统计与舒适噪声估计要连续，恢复后继续推帧的增益轨迹与中断前一致。

验收：go test ./... 的 15 条用例必须全绿，不许改动测试、跳过或按固定输入写死；并发与重启各自构造场景验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块

- 模块路径：gsb/batch48-go-game-c-06
- 包名：task
- 源文件：residual_echo_suppressor.go
