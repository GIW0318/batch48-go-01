# 帧同步输入延迟协商：双端取大、帧边界切换与卡帧归因

## 题面

帧同步对局上线后，两端手感长期不一致：一边觉得操作跟手，另一边觉得按下去半天才动；开团掉帧之后延迟自己涨上去就再也降不下来，观战端明明不卡却一直被拖着走；重连回来的玩家一进来就把延迟提案砸到 0，全场跟着一起卡；运维修想定位是谁的锅，日志里只有一句帧超时；更麻烦的是同一场对局的两份观战录像里延迟曲线对不上，复盘时谁也说不清当时到底延迟几帧。落点 gsb/batch48-go-game-a-15 的 input_delay_negotiator.go，类型 InputDelayNegotiator，签名保持：NewInputDelayNegotiator(maxDelay int) *InputDelayNegotiator、Propose(peerID string, frames int) error、Proposal(peerID string) (int, bool)、Agreed() int、Drop(peerID string) error、Commit(frame uint64) (int, error)、Confirm(frame uint64) error、Current() int、ObserveStall(frame uint64, ms int, cause StallCause)、StallCount(cause StallCause) int、SetRecoveryWindow(n int)、RecoveryWindow() int、MaxDelay() int、Peers() []string、Snapshot() ([]byte, error)、Restore([]byte) error。

要修：Propose 拒绝空 peer ID、负帧数与超过上限的提案，被拒绝的提案不得落地；第一次 Commit 之前提案可以随意改动，提交之后再改一律按运行中调整处理；Drop 之后该 peer 的提案不再计入协商值，重复 Drop 返回 ErrPeerNotFound；协商值取所有在线提案中的较大值，提案变更只在 Commit 的帧边界生效，首次提交前 Current 为 0；Commit 的帧号必须递增，重复或回退返回 ErrCommitRegressed；运行中上调立即在下一个帧边界生效，下调需要连续 N 次无卡顿的提交才降一帧，N 由 SetRecoveryWindow 设置且小于 1 时按 1 处理；卡帧达到 100 毫秒才计入归因，低于阈值的观测既不计次数也不打断达标计数，帧号为 0 的观测一律忽略；卡帧超阈值要记录责任方并触发一次上调，只有输入缺失才上调，渲染慢与网络抖动不上调；上调到 MaxDelay 后不再增加，超出的卡帧只计次数；Confirm 只能确认已提交的帧，超前返回 ErrConfirmAhead，回退返回 ErrConfirmRegressed，重复确认幂等；Peers 按 ID 升序；一方向上调、另一方向下调同时发生时，最终值必须一致且取较大值，不能各自停在不同的值；重连后新连接的提案不能把当前延迟降到低于已提交帧深度；连续卡帧但网络指标正常时要区分输入缺失与渲染慢，只有输入缺失才上调延迟；Snapshot 与 Restore 要保存当前延迟、提案、达标计数与卡帧归因，坏数据返回 ErrBadSnapshot 且不改动现有状态。

硬约束：Propose 与 Commit 会被两端会话 goroutine 并发调用，提案、提交帧与达标计数只能在同一把锁下推进；上调与下调都必须以帧边界为单位落地，不允许半帧修改；同一帧上的重复提交与并发提案不得让协商值来回抖动，回放同一串事件必须得到同一个延迟值；重启后不得重新解释已经提交过的输入，恢复出来的延迟不得低于提交帧深度。验收：go test ./... 15 条全过，测试不得修改、跳过或按固定输入写死；并发升降档与重启恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-game-a-15`
- 实现文件：`input_delay_negotiator.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`InputDelayNegotiator`、`StallCause`
