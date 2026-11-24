# 任务包 batch48-go-game-a-19

## 模块

- module: gsb/batch48-go-game-a-19
- package: task
- file: input_redundancy_framer.go

## 题面

格斗游戏的对战服在弱网下频繁出现输入丢失：玩家在同一帧连按两次防御，服务器只收到第一次，第二次凭空消失；有人在结算动画期间切出去三十秒，回来后整局卡在等待齐备，对面一直停在上一帧；还有观战转上场的玩家一进来就被要求补齐开场以来的全部帧，缓冲瞬间撑爆，进程随后重启。复盘抓包时还能看到同一帧的副本在三个包里各带一份，接收端却按到达顺序让后到的覆盖了先到的，冲突记录一条都没有。落点 gsb/batch48-go-game-a-19 的 input_redundancy_framer.go，类型 InputRedundancyFramer，签名保持：NewInputRedundancyFramer(redundancy int) *InputRedundancyFramer、SetRedundancy(n int) error、SetBufferLimit(n int) error、SetAFKGrace(n int) error、AddPlayer(player PlayerID, joinFrame uint64) error、RemovePlayer(player PlayerID) error、Encode(frame uint64, in Input) ([]Packet, error)、Ingest(p Packet) (frame uint64, in Input, ok bool, err error)、Ready(frame uint64) bool、DropBefore(frame uint64)、PendingFrames(player PlayerID) int、Stats() FramerStats、Snapshot() ([]byte, error)、Restore(payload []byte) error。

要修：每帧输入编成当前包加后续若干包的冗余副本，Encode 返回 n 个副本，副本序号从 0 递增，帧号与输入一致，副本序号只用于诊断，不参与去重，冗余窗口限定在 1 到 8；未注册玩家编码或收包返回 ErrPlayerUnknown，重复注册返回 ErrPlayerExists，帧号为 0 返回 ErrInvalidFrame；接收侧按玩家与帧去重，完全相同的副本只交付一次，其余计入 Duplicates 且不报错，去重键固定为玩家与帧的组合；同一玩家同一帧出现不同输入按冲突处理，返回 ErrInputConflict，先到的版本保留；一帧齐备要求所有活跃玩家都已收到该帧，任一玩家缺席时 Ready 为 false；齐备判定按帧独立，收到第 5 帧不会让第 6 帧提前齐备；每玩家待组装帧数受上限约束，超限的新帧返回 ErrBufferFull，默认上限 64，上限按玩家分别计算，不能用一个全局计数代替；DropBefore 丢弃更早的待组装帧，每玩家待组装计数同步下降；移除玩家后其不再参与齐备判定，该玩家遗留的待组装帧一并清理且不影响其他玩家，统计中的玩家数同步减一；玩家长时间无输入时按空输入参与齐备，不能永远等不到齐备，宽限帧数默认 3；冗余副本里的旧帧已被消费时只更新统计，不重复交付；中途加入的玩家只从其加入帧开始参与齐备，不要求补齐全场历史帧。

硬约束：并发 Ingest 由互斥锁保护，同一玩家同一帧只能有一个版本生效，重复副本只计一次；每玩家待组装帧数与冗余窗口硬性封顶，任何路径都不得越过；快照保存已收帧与齐备游标，恢复后待组装状态与齐备判定口径必须完整。

验收：go test ./... 15 条全过，不得改测试、跳过或按固定输入写死；并发与重启恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```
