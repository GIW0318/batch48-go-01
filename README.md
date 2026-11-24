# 每客户端快照发送预算：优先级累积、预算截断与增量降级

## 题面

正式服开团战的时候，同一场对局里有人看到队友凭空消失又出现，有人看到已经跑远的敌人还在原地站着当幽灵；弱网玩家的技能提示卡住不动，问他才知道自己的状态包根本没发出去；还有人重连后画面一直是花的，因为服务端一直发增量，而他的基线包早就丢了。落点 gsb/batch48-go-game-a-14 的 snapshot_send_governor.go，类型 SnapshotGovernor，签名保持：NewSnapshotGovernor(tickRate int) *SnapshotGovernor、SetBudget(clientID string, bps int) error、Budget(clientID string) int、MarkDirty(clientID string, entity EntityID, priority int) error、MarkSelf(clientID string, entity EntityID) error、MarkAlly(clientID string, entity EntityID) error、Leave(clientID string, entity EntityID) error、Build(clientID string, tick uint64) (PacketPlan, error)、Ack(clientID string, tick uint64) error、Acked(clientID string) uint64、Pending(clientID string) int、Snapshot() ([]byte, error)、Restore([]byte) error。

要修：SetBudget 拒绝空客户端 ID 与非正的每秒预算；未设置预算的客户端建包与确认都返回 ErrUnknownClient；实体 ID 为 0 返回 ErrInvalidEntity；每 tick 可用字节等于每秒预算除以 tick 率后整除，包头 16 字节、每条实体记录 24 字节、每条销毁记录 8 字节，Bytes 必须按这三项精确累加且不得超过每 tick 预算；脏实体按有效优先级降序打包，优先级相同时按实体 ID 升序，装不下的实体进入 Skipped 并按 ID 升序报告；每被跳过一次，有效优先级加 10，发送后清零；脏标记只在对应 tick 的包被确认后清除，未确认的包不能当作后续增量基线；还没有任何确认基线时必须发全量并把 Baseline 置 0，确认之后发增量并把 Baseline 设为已确认 tick 并把 Full 置假；Ack 只前进不回退，重复或迟到的确认不改动状态；MarkSelf 与 MarkAlly 只登记归属，不替客户端打脏标记；Build 返回的 Tick 等于入参 tick，计划里不得出现重复实体 ID；Pending 统计脏实体数量，正在销毁的实体不计入；实体离开兴趣范围时要在同一份包内发出销毁通知，且该实体不再按普通记录重复发送；预算极小时必须保证玩家自身状态可发，必要时牺牲其他所有实体；增量基线帧丢失后要自动降级为全量帧，不能发送无法解码的增量；实体离开范围时即使预算已满也要保证销毁通知发出，可以从低优先级实体借用额度并记账；Snapshot 与 Restore 要保存每客户端预算、脏集合与优先级累积，坏数据返回 ErrBadSnapshot 且不改动现有状态。

硬约束：Build 与 Ack 会被每 tick 的广播 goroutine 和网关确认 goroutine 同时调用，预算、脏集合与基线只能在同一把锁下推进；每 tick 字节数是硬顶，只有销毁通知允许借用额度并必须在计划里可见；同一份计划在相同输入下必须逐字节稳定，排序不得依赖 map 遍历顺序；重启后不得把已确认的实体重新当成脏实体再发一遍。验收：go test ./... 15 条全过，测试不得修改、跳过或按固定输入写死；并发建包与重启恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-game-a-14`
- 实现文件：`snapshot_send_governor.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`SnapshotGovernor`、`PacketPlan`、`EntityID`
