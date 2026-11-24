# 客户端输入上行中继的按帧去重、乱序缓冲与确认水位

## 题面

高速网络下客户端输入上行出现两种怪事：同一帧的操作被下游消费了两次，角色原地平移了一下又弹回；弱网玩家重连补发之后，已经确认过的帧又被重放，技能连放两次。观战端也报告过同一玩家出现两套并行位移，回放里还能看到已确认帧和未确认帧混在一起投递。这一版要把每帧只投一次、按帧号归位和确认水位收在同一个中继里，水位只进不退，补发只补未确认的部分。落点 gsb/batch48-go-game-a-10 的 input_queue_relay.go，类型 InputQueueRelay，签名保持：NewInputQueueRelay(perPlayerCap int, lateWindow uint64) *InputQueueRelay、Push(playerID string, frame uint64, in Input) (PushResult, error)、Ack(playerID string, frame uint64) error、Flush(playerID string, max int) []Input、Window() (uint64, uint64)、Dropped(playerID string) int、Pending(playerID string) int、Resend(playerID string) []Input、HighestAcked(playerID string) uint64、Snapshot() ([]byte, error)、Restore([]byte) error。

要修：Push 拒绝帧号为 0 与空玩家 ID，分别报 ErrInvalidFrame 与 ErrInvalidPlayerID，合法帧从 1 开始计数；每玩家队列按 perPlayerCap 封顶，满了报 ErrQueueFull 并保留旧输入不覆盖、顺序不变，被拒绝的 Push 不计入 Buffered；同一玩家同一帧重复上报去重，第二次返回 Accepted 为假、Duplicate 为真，队列里只留一份，Pending 与 Buffered 都不增加；乱序输入按帧号归位，Flush 严格升序返回并受 max 限制，max 为负按 0 处理，取走后对应帧从队列移除，再次 Flush 不重复返回；Ack 推进确认水位，水位只前进不回退，已确认的帧不再 Flush，迟到的旧 Ack 不报错也不改变水位；落后于最高帧且超出迟到窗口的输入直接丢弃并计数，返回 Accepted 为假且不得标成 Duplicate，窗口内的迟到帧照常入队且不计数；每玩家队列互相隔离，一名玩家的 Flush 不影响另一名玩家，全局水位取所有在线玩家中最慢的那个，最高帧取全局最大；同一玩家同一帧先到的低优先级输入与后到的高优先级输入按规则合并，以 Merged 标记，不得简单覆盖；迟到窗口内到达但下游已消费的帧号要计数并回执，不得静默丢弃；断线补发时下游已确认的帧不得重放，未确认的帧按原帧号补一次，Resend 不清空队列也不改水位；单帧输入体积受 MaxSingleInputBytes 约束、每玩家队列容量受 MaxPerPlayerQueue 约束，超出按错误返回；快照保存未确认输入、水位与丢弃计数，重启后水位与最高帧不变，未确认输入不得重复投递给下游，只返回尚未投递的帧，坏快照报 ErrBadSnapshot 且不改动现有状态。

硬约束：同一玩家同一帧的并发 Push 只能产生一份有效输入，重复方拿 Duplicate；水位单调，Ack 迟到与重启恢复都不得把水位推回；队列与单帧体积设硬顶，超出按错误返回而不是覆盖已有输入；队列、水位与计数共用一把互斥锁，读方不得看到半更新状态。

验收：go test ./... 15 条全过，测试不得修改、跳过或按固定输入写死；并发重复上报、乱序归位与重启恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-game-a-10`
- 实现文件：`input_queue_relay.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`InputQueueRelay`
