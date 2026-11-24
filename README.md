# 客户端预测与服务器校正：输入环缓冲、回滚重放与误差平滑

## 题面

客户端预测上线后，操作手感变成两种极端：轻点方向键角色先滑出去半米再被拉回，连续按两下技能会放两次；服务器纠正包一跳帧，角色就直接瞬移到很远的位置，之后几秒都在往回飘；重放窗口撑大以后，断线重连的玩家又把早已确认的输入补了一遍，技能凭空多放一次。落点 gsb/batch48-go-game-a-12 的 prediction_reconciliation_ledger.go，类型 PredictionLedger，签名保持：NewPredictionLedger(window int) *PredictionLedger、Record(frame uint64, in Input) error、Acknowledge(frame uint64, server State) error、Replay() (State, int, error)、SetSmoothing(alpha float64)、Smoothing() float64、SetDivergenceThreshold(v float64)、DivergenceThreshold() float64、Pending() int、LastAcked() uint64、Generation() uint64、Predicted() State、Snapshot() ([]byte, error)、Restore([]byte) error。重放窗口以帧为单位，未收到任何服务器状态时基准状态为零值。

要修：Record 拒绝帧号 0，返回 ErrInvalidFrame；已确认的帧号不得重新记录，返回 ErrAckedFrame；同一帧重复记录返回 ErrDuplicateFrame 且只保留一份；帧号必须递增，回退的帧号返回 ErrOutOfOrderFrame；未确认输入数超过重放窗口时返回 ErrReplayWindowOverflow，同时丢弃全部历史并直接采信服务器状态；Acknowledge 之前的输入不得再参与重放；Acknowledge 的误差按服务器状态与本地在该确认帧上的预测状态比较，误差大于阈值直接吸附到服务器状态，误差在阈值内按平滑系数收敛，平滑系数非法时夹到 0 与 1 之间；同一帧重复确认幂等，不得把后到的另一份服务器状态再叠一次；位移为 0 的空输入也要照常记录，用来推进帧号并保持回放步长一致；Pending 只统计未确认条目，确认推进后立即下降；Replay 返回当前状态与本次重放的帧数，Predicted 与 Replay 的结果必须一致；确认帧跳跃（中间帧的确认丢失）时，被跳过的帧视为已确认并从环形缓冲移除，不得重复重放；重放过程中收到更新的服务器状态要中断当前重放并以新状态为基准，不能把两份校正叠加；输入缓冲回绕后帧号会被复用，旧条目必须按世代号区分，不能被当成新输入重放；Generation 在每次真正推进确认后自增；Snapshot 与 Restore 要保存未确认输入、基准状态与平滑参数，坏数据返回 ErrBadSnapshot 且不改动现有状态。

硬约束：Replay 由互斥锁与原子世代号保护，重放期间到达的新状态必须让本次重放中断并返回 ErrReplayInterrupted；重放只依赖已记录输入与基准状态，不得读取当前时间、随机数或外部配置，同一组输入必须得到逐位一致的结果；环形缓冲容量与重放窗口都是硬顶，任何路径都不得越界；同一帧的重复确认与并发的 Record 不得交叉污染基准状态；恢复后未确认输入不得被重复应用到已确认的帧上。验收：go test ./... 15 条全过，测试不得修改、跳过或按固定输入写死；并发确认与重启恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-game-a-12`
- 实现文件：`prediction_reconciliation_ledger.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`PredictionLedger`、`State`、`Input`
