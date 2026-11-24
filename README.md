# 任务包 batch48-go-game-a-22

## 模块

- module: gsb/batch48-go-game-a-22
- package: task
- file: cast_interrupt_guard.go

## 题面

读条系统改版后，施法条的推进与打断乱成一团。炎爆有时读满才放出去，有时刚读到一半就凭空消失；读条被推一下之后，进度条不是接着走而是从头重读；引导技能最后一跳与打断在同一毫秒到达时，跳数统计多一少一，事后对账怎么也对不上；手快的玩家连按两次施法键，界面上叠出两条施法条，两条各自读数各自结束。沉默到期后角色仍被拒绝施法，必须再挨一次沉默才恢复；副本进程重启后，没读完的施法直接不见，已经放出去的跳数却重复计入结算。落点 gsb/batch48-go-game-a-22 的 cast_interrupt_guard.go，类型 CastInterruptGuard，签名：NewCastInterruptGuard(Options) *CastInterruptGuard、Begin(CastRequest, int64) (Cast, error)、Advance(int64) ([]Tick, error)、Interrupt(string, int64) error、Finish(int64) (CastResult, error)、PushBack(int64, int64) error、Silence(int64) error、Active() (Cast, bool)、LastResult() (CastResult, bool)、SilenceRemaining(int64) int64、Snapshot() ([]byte, error)、Restore([]byte) error。

要修：
一、法术名为空、读条时长非正、引导技能缺跳数或跳间隔非正，Begin 报 ErrInvalidRequest。
二、同一角色已有施法时再次 Begin 报 ErrCastInProgress，原施法继续推进，不得被顶掉或叠出第二条。
三、沉默窗口内 Begin 报 ErrSilenced；沉默到期的边界时刻即可正常施法；SilenceRemaining 按毫秒返回剩余量，到期返回 0。
四、瞬发法术（CastMs 为零）在 Begin 的同一毫秒完成，EndsAt 等于发生时刻，不留活动施法；随后 Finish 返回已完成，EndedAt 仍是那个完成时刻。
五、读条未到完成时刻就调用 Finish 报 ErrCastIncomplete，活动施法原样保留。
六、引导技能按 TickMs 固定间隔出跳，跳序号从 0 递增；Advance 只返回本次新产生的跳，同一毫秒重复调用返回空，不得重复出跳。
七、引导 Finish 带回本次施法累计出过的跳数，包含同一毫秒内先结算的那一跳。
八、没有活动施法时 Interrupt 报 ErrNoActiveCast；打断成功后 LastResult 反映未完成、原因与打断时刻。
九、PushBack 只接受正数，否则报 ErrInvalidPush；推后完成时刻后移，原完成时刻点 Finish 报 ErrCastIncomplete；累计推条超过 Options.MaxCastMs 时按上限截断，已积累的读条进度不清零重来。
十、快照恢复未完成施法、已出跳数、下一跳序号与沉默窗口；恢复后继续 Advance 只出剩余跳，Finish 的跳数累计正确。
十一、坏快照报 ErrBadSnapshot，当前施法与沉默状态不被改动。
十二、最后一跳与打断落在同一毫秒时，先结算该跳再判打断，结算顺序固定。

硬约束（并发与原子性）：Advance 与 Interrupt 来自不同协程，同一时刻只有一个施法在推进，出跳与跳数记账必须一次提交，任意并发下每一跳只对外出现一次；查询 Active 与快照只能读到完整的施法状态，不允许读到跳数加了但序号没动的中间态。施法时长与跳数都是硬顶，不得超过 MaxCastMs 与 MaxTickCount。

硬约束（快照与重启）：快照与写入互斥，恢复要么整套生效，要么整套不生效；重启后未完成的施法要能按原完成时刻继续推进，不能出现施法回来了但跳数清零、或者跳数还在却重复出跳的情况。

验收：go test ./... 15 条全过，不改测试、不跳过、不写死；并发与重启自验；仅本地实现。

## 运行方式

```bash
go test ./...
```
