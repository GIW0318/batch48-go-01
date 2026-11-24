# 加密数据报通道的多纪元防重放

## 题面

安全运营回放一段加密语音通道时发现三类怪事：同一段数据报被原样灌回来，值班平台只记了一条重复，随后整段流量又被判成合法；换钥窗口里旧纪元的包被一律丢弃，业务侧听到的是两秒断续；链路抖动后对端序号跳到十几万之后，接收端直接把窗口拉过去，中间丢掉的几百个包再也没机会重传。落点 gsb/batch48-go-safety-a-23 的 datagram_replay_warden.go，类型 ReplayWarden，签名保持：NewReplayWarden(lim Limits) *ReplayWarden、Accept(epoch uint32, seq uint64, now time.Time) (Verdict, error)、AdvanceEpoch(newEpoch uint32) error、RequestResync(fromSeq uint64) error、ApplyResync(sessionID string, base uint64) error、CurrentEpoch() uint32、Highest(epoch uint32) (uint64, bool)、WindowSeen(epoch uint32, seq uint64) bool、Stashed() int、Duplicates() uint64、Snapshot() ([]byte, error)、Restore(payload []byte) error。

要修：64 位滑动位图与最高序号没有一起维护，窗口内已收过的序号再次到达仍被判为接受，重复计数不涨；乱序包落后窗口 64 位以上仍然放行，而窗口边缘合法的乱序包又被误判重放；最高序号向前跳跃时位图没有整体移位，旧位残留让紧随其后的新序号被判成重复；首次收包没有正确建立窗口，最高序号为零的边界把整段窗口当成空；未来纪元的包被当作当前纪元处理；AdvanceEpoch 接受相同或更小的纪元号，窗口随之回退；换钥后旧纪元在宽限期内没有按旧窗口判定，宽限期结束后又继续放行；超出纪元容忍代数或宽限期的包被记成重复而不是过期；收到远超窗口的序号时直接推进窗口，丢掉的中间包既没有重传机会也没有记录；重同步期间没有把后续包放进有界暂存区，或者暂存区无上限增长；重同步完成后暂存包没有按新基准重新判定，超限的包没有计入丢弃；ApplyResync 允许空会话号、倒退的基准，或在没有请求重同步时直接生效；RequestResync 接受零基准，重复调用报错；快照只保存最高序号不保存位图，故障切换后窗口回退；位图与最高序号不一致的快照被截断后照样恢复，把窗口改成半截；多个收包协程并发调用 Accept 时位图推进不是原子操作，重复包会被放行两次。

硬约束：Accept、AdvanceEpoch、RequestResync、ApplyResync 在同一把锁下串行，位图与最高序号的推进、重复计数必须原子，并发收包不能出现同一序号两次接受；窗口只允许单调前移，回退的基准、旧纪元与未来纪元一律按显式错误或对应判定拒绝；暂存区容量硬性封顶，重同步期间超出的包只能计数丢弃，不能静默扩容；纪元号单调递增，只保留容忍代数内的旧窗口，宽限期按换钥时刻计算。

验收：go test ./... 15 条全过，不得改测试、跳过或按固定输入写死；并发与重启恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-a-23`
- 实现文件：`datagram_replay_warden.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`ReplayWarden`、`Verdict`、`Limits`
