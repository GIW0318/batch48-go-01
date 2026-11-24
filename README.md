# 对局断线重连：一次性恢复令牌、宽限期占用与补发窗口

## 题面

派对游戏上线后，断线重连每隔两周就有一次投诉：玩家掉线三秒内回来，角色却已经被别人接管，队友眼睁睁看着自己的角色替对手推塔；也有人重连后视野被拉回出生点、队伍变成旁观者、对局阶段退回准备厅；还有玩家在宽限期内退了房又开新局，旧的重连链接照样把他拉回上一局，结算数据全乱。落点 gsb/batch48-go-game-a-13 的 reconnect_handoff_ledger.go，类型 ReconnectLedger，签名保持：NewReconnectLedger(grace time.Duration, resendWindow int) *ReconnectLedger、Join(playerID, team string, phase MatchPhase) error、Detach(playerID string) (ResumeToken, error)、Resume(token ResumeToken, conn Conn) (*Session, error)、Abandon(playerID string)、Expire(now time.Time) []string、Confirm(playerID string, frame uint64) error、AcceptResend(playerID string, frame uint64, in Input) error、SetCamera(playerID string, c Camera) error、SetPhase(playerID string, phase MatchPhase) error、Lookup(playerID string) (Session, bool)、ResendFrom(playerID string) uint64、Pending() int、Denials() int、Snapshot() ([]byte, error)、Restore([]byte) error。

要修：Join 拒绝空玩家 ID 与重复入场；Detach 只在玩家在线时成功，返回一次性令牌并把会话标记为宽限期、清空连接 ID，重复 Detach 返回 ErrPlayerInGrace；宽限期内实体保持占用，Pending 只统计宽限期内的玩家，宽限截止时间以 Detach 那一刻加宽限期计算；Resume 校验令牌，未知令牌返回 ErrTokenInvalid，已用令牌返回 ErrTokenUsed，过期令牌返回 ErrTokenExpired，不能笼统报成不存在；恢复要带回队伍、相机、对局阶段与最后确认帧，并把补发起点设成最后确认帧加一；同一令牌只能成功恢复一次；Confirm 的帧号只增不减，回退返回 ErrConfirmRegressed；补发只接受最后确认帧之后、窗口之内的帧，落后于确认帧或超出窗口一律返回 ErrResendOutOfWindow，被接受的补发计入 Resent，窗口边界上的帧照常接受；宽限期到期由 Expire 释放实体并返回按玩家 ID 升序排列的名单，令牌标记为已过期而不是删除；Expire 可以被定时器反复调用，第二次调用不得把已经释放的玩家再返回一遍；玩家在宽限期内主动放弃并开新局后，旧令牌必须立即失效，不能恢复回原对局；重连时对局已经结束，要返回明确的终局状态并清理令牌，不能把玩家重新拉进对局；同一账号两个连接用不同令牌并发重连时只能一个成功，另一个要被踢回并计入拒绝计数；Snapshot 与 Restore 要保存宽限截止时间与令牌使用标记，重启后已用令牌不得复活，坏数据返回 ErrBadSnapshot 且不改动现有状态。

硬约束：Detach 与 Resume 会被多个网关 goroutine 并发调用，会话、令牌与拒绝计数只能在同一把锁下推进；同一账号的令牌校验与实体占用必须原子完成，不允许两个连接同时拿到同一份会话；重连状态不允许依赖进程内的临时变量，重启后必须能从快照原样还原；拒绝计数只增不减，被拒绝的连接不得改动任何会话状态。验收：go test ./... 15 条全过，测试不得修改、跳过或按固定输入写死；并发重连与重启恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-game-a-13`
- 实现文件：`reconnect_handoff_ledger.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`ReconnectLedger`、`Session`、`ResumeToken`、`Conn`、`MatchPhase`
