# 任务包 batch48-go-game-a-17

## 模块

- module: gsb/batch48-go-game-a-17
- package: task
- file: prop_authority_transfer.go

## 题面

竞技射击服的拾取与投掷道具在有玩家换手时出了连环事故：两名玩家同时抢同一把武器，服务器接受了两次转移，归属在两帧之间来回跳；换手成功之后，接手方在确认前预输入的动作被整段吞掉，手感像凭空多了半秒延迟；老持有者在客户端缓存里重发旧帧输入，反作弊直接判踢，赛后申诉说只是网络重连；还有一局在转移进行中道具被销毁，此后所有转移请求都返回成功，却没人真正拿到道具。落点 gsb/batch48-go-game-a-17 的 prop_authority_transfer.go，类型 AuthorityTransfer，签名保持：NewAuthorityTransfer() *AuthorityTransfer、RegisterProp(entityID EntityID, owner PlayerID, seq uint64) error、Request(entityID EntityID, from, to PlayerID) (TransferID, error)、Commit(entityID EntityID, atSeq uint64) error、Input(entityID EntityID, owner PlayerID, seq uint64, cmd Command) error、Cancel(transferID TransferID) error、Destroy(entityID EntityID) error、Owner(entityID EntityID) (PlayerID, error)、Pending(entityID EntityID) (TransferID, bool)、Buffered(entityID EntityID) int、RejectedInputs(entityID EntityID) uint64、Suspicion(player PlayerID) int、Stats() TransferStats、Snapshot() ([]byte, error)、Restore(payload []byte) error。

要修：注册道具记录初始持有者与序号，重复注册返回 ErrEntityExists；转移请求只接受当前持有者发起，发起者不符返回 ErrNotOwner，转给自己返回 ErrSelfTransfer，已有待提交转移返回 ErrTransferPending，成功时返回自增且不复用的 TransferID 并让待提交转移数加一；提交前旧持有者的输入仍然生效，序号须严格大于已生效序号，重复或更小返回 ErrStaleInput；提交前新持有者的输入不立即生效，按序号进缓冲，Buffered 返回条数，无关玩家输入返回 ErrNotOwner；Commit 要求 atSeq 严格大于缓冲中全部序号，否则返回 ErrCommitSeqInvalid 且转移保持待提交，提交成功后归属切到新持有者、缓冲清空、Pending 为 false；提交后旧持有者的输入一律返回 ErrNotOwner 并累加 RejectedInputs，新持有者序号不大于已生效序号时返回 ErrStaleInput；缓冲达到上限后新输入返回 ErrInputBufferFull，不得覆盖已有条目，默认上限 32；Cancel 按 TransferID 取消，归属保持原主、缓冲清空，未知或已完成的 TransferID 返回 ErrTransferUnknown；提交序号之前新持有者已发出的输入必须在提交后按序重放，不能直接丢弃，重放条数计入 Stats 的 Replayed；转移期间道具被销毁时，所有待处理转移一律失败并清理缓冲，之后对该道具的输入与查询返回 ErrEntityUnknown；转移后旧持有者重发的旧序号输入计入拒绝与重放计数，但不增加 Suspicion，反作弊踢出只看其他异常；快照保存归属、待提交转移与输入缓冲，恢复后未完成的转移不再存活，对其 Commit 返回 ErrNoPendingTransfer，原持有者保持归属。

硬约束：所有入口在同一把锁下串行，Request 与 Commit 的归属切换必须原子，同一道具的并发转移只能有一个成功，失败方不得改动归属与缓冲；缓冲条数与统计计数硬性封顶；快照必须自包含，恢复后不得出现半生效归属。

验收：go test ./... 15 条全过，不得改测试、跳过或按固定输入写死；并发与重启恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```
