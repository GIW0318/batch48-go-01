# 掉线席位保留与一次性重连凭证

## 题面

凌晨一场排位赛里，玩家掉线重连后发现自己坐到了别的椅子上，另一名玩家拿着同一张重连凭证进了两次场；值班同学手动清掉席位之后，先前发出去的凭证还能把人放进已经开打的房间。客服复盘又补了两条：保留窗口明明还没到点，掉线玩家却被告知座位已经释放；凭证过了有效期仍然能兑换，兑换失败还会把席位清掉。这一版要把座位表、保留表和凭证表收在同一个守卫里，重连必须在原房间、原座位完成，不能换座，也不能凭旧凭证二次入场。落点 gsb/batch48-go-game-a-06 的 reconnect_seat_guard.go，类型 ReconnectSeatGuard，接口按现有签名：NewReconnectSeatGuard(holdWindow, tokenTTL time.Duration) *ReconnectSeatGuard、Join(roomID, playerID string, seatIndex int) (Seat, error)、OnDrop(roomID, playerID string) (SeatHold, error)、IssueResume(roomID, playerID string) (ResumeToken, error)、Resume(token string) (Seat, error)、Expire(now time.Time) []Seat、Release(roomID, playerID string) error、Seat(roomID, playerID string) (Seat, bool)、HeldSeats() []Seat、Snapshot() ([]byte, error)、Restore([]byte) error。

要修：加入时同一玩家只能占一个座位，重复加入报 ErrAlreadySeated，座位被占报 ErrSeatTaken，座位号只接受 0 到 MaxSeatIndex（含两端），越界报 ErrInvalidSeat，被拒绝的加入不得留下任何座位记录；成功加入返回 State 为 SeatActive 的座位，Seat 能按房间与玩家查回同一个座位号和归属；掉线只对已入座的玩家生效，未入座报 ErrPlayerNotFound，掉线后席位进入保留态，保留截止严格等于掉线时刻加保留窗口，OnDrop 必须返回带房间、玩家、座位号与截止时间的 SeatHold，此时 Seat 读到的是 SeatHeld；保留窗口与凭证有效期各自配置、互不推导，凭证到期只废凭证，不影响席位保留截止；只有保留态席位才能签发凭证，活动席位签发报 ErrSeatNotHeld，签出的凭证非空并绑定房间与玩家，到期时间按凭证有效期计算而不是保留窗口；凭证一次性使用，兑换成功返回原座位且状态回到 SeatActive，已用过的再提交报 ErrTokenUsed，不认识的凭证报 ErrTokenNotFound，超过有效期的报 ErrTokenExpired 且席位继续保留、仍出现在 HeldSeats 里；Expire 在截止时刻（含）之后释放席位并返回释放结果，返回项 State 为 SeatReleased，未到期不释放，同一席位只上报一次，重复调用返回空结果；释放后的席位允许他人加入，座位号不变但归属换人；主动退出必须立即释放席位、不进入保留窗口，也不签发重连凭证；同一席位在保留窗口内再次掉线时只保留一枚有效凭证，旧凭证作废；席位释放后先前签发的凭证不得再当作入场凭证使用；HeldSeats 按房间号、再按座位号升序返回，不同房间的保留互不干扰；快照保存保留截止与凭证摘要，重启后已保留席位、截止时间与凭证状态原样恢复，Expire 按原截止释放，坏快照报 ErrBadSnapshot 且不改动现有状态。

硬约束：同一座位并发加入、同一凭证并发兑换都只能有一个成功，其余必须拿到明确错误；快照与写入互斥，读到的只能是全量旧状态或全量新状态；席位表、保留表与凭证表设硬顶（凭证表上限为 MaxResumeTokens），超出按错误返回而不是覆盖已有条目；所有截止判断走同一可替换时钟，注入时钟后结果必须可复现。

验收：go test ./... 15 条必须全过，测试不得修改、跳过或按固定输入写死；并发加入、凭证并发兑换与重启恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-game-a-06`
- 实现文件：`reconnect_seat_guard.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`ReconnectSeatGuard`
