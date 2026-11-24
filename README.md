# 任务包 batch48-go-game-a-25

## 模块

- module: gsb/batch48-go-game-a-25
- package: task
- file: combo_counter_guard.go

## 题面

连击系统上线后，近战伤害忽高忽低。同样打到第三段，有时按三段加成结算，有时只按一段算；打两下停一下，段数该断不断，界面上倒计时却已经清零；被控两秒期间一次没累加，控制一结束窗口却从头开始，玩家感觉自己的连击被凭空清空。切目标时更乱：有人切过去段数归零，有人切过去段数还在但计时没重开，同一套操作两种结果。还有玩家发现，窗口只剩最后几毫秒时打出的命中，结算时加成已经归零；反复刷新连击面板，段数在 1 和 0 之间闪。落点 gsb/batch48-go-game-a-25 的 combo_counter_guard.go，类型 ComboCounterGuard，签名：NewComboCounterGuard(Options) *ComboCounterGuard、Hit(string, string, int64) (Combo, error)、Break(string, string, int64) error、Bonus(string, int64) int、Extend(string, int64) error、Transfer(string, string, string) error、Control(string, int64) error、Count(string, int64) int、ExpiresAt(string, int64) int64、Snapshot() ([]byte, error)、Restore([]byte) error。

要修：
一、玩家名或目标为空、时刻为负，Hit 报 ErrInvalidRequest。
二、命中落在窗口内时把段数加一，上限 Options.MaxCombo；结果带回玩家、目标、段数、加成、到期时刻与最后命中时刻。
三、加成按段数乘 BonusStepPct 计算，由 MaxBonusPct 封顶，同一时刻查询与命中返回的加成一致。
四、超时、被控、切目标与受击各按规则断连：自然超时会清零段数，受击与显式 Break 同样按原因清零段数；切换目标保留段数、只把窗口重新起算，段数不清零而计时清零；被控期间命中既不累加也不立即断连，控制结束后窗口计时接着走。
五、Break 只接受 timeout、stun、damage 三种原因，其它原因报 ErrInvalidReason 且段数不变；Break 与 Transfer 对未登记玩家报 ErrUnknownPlayer。
六、Extend 只接受正数，零或负数报 ErrInvalidExtend；延长到期时刻但不清零段数。
七、未登记玩家的 Bonus、Count 返回 0，ExpiresAt 返回 0，不 panic。
八、到期时刻本身仍算窗口内，过期后的第一毫秒起段数与加成归零；过期后再命中从一段重新起算。
九、窗口内查询 Bonus、Count、ExpiresAt 看到的必须是同一份状态，段数与到期时刻不得互相矛盾。
十、快照往返段数、目标、到期时刻、延长量与控制窗口；恢复后继续命中能从原段数递增。
十一、坏快照报 ErrBadSnapshot，现有连击状态原样保留。
十二、同一毫秒到达的多段命中按调用序号逐段结算，每段加成按递增后的段数取，不能全部按同一段数计算。

硬约束（并发与原子性）：同一玩家的 Hit 与 Break 并发时，段数只递增或清零一次，不得出现半次结算；查询只读到整份旧状态或整份新状态。每个玩家一份独立状态，玩家之间不共享锁，窗口、段数与加成上限都是硬边界。

硬约束（快照与重启）：快照与写入互斥，恢复要么整套生效要么整套不生效；重启后段数、到期时刻与控制窗口都要能继续推进，被控中的玩家恢复后仍不清零。

验收：go test ./... 15 条全过，不改测试、不跳过、不写死；并发与重启自验；仅本地实现。

## 运行方式

```bash
go test ./...
```
