# 对战房间的席位、预留与房主顺位

## 题面

开服第一小时，一局 5v5 房间在房主点开始后被第七个玩家挤进来，同一张椅子坐两个人；房主掉线后房间显示无主，剩下四人没法开下一局。落点 gsb/batch48-go-game-a-01 的 room_lifecycle_guard.go，类型 RoomLifecycleGuard，签名保持：NewRoomLifecycleGuard(maxRooms int) *RoomLifecycleGuard、Create(ownerID string, seats int) (Room, error)、Join(roomID, playerID string) (Seat, error)、Reserve(roomID, playerID string, ttl time.Duration) (Seat, error)、Start(roomID string) error、TransferHost(roomID string) (string, error)、Snapshot() ([]byte, error)、Restore([]byte) error，余下按注释保持签名。

要修：单房席位限 2 到 8，越界报 ErrInvalidSeats，房间超限报 ErrRoomLimitReached，销毁后释放名额；加入落最小空位，重复加入报 ErrAlreadyJoined，满员报 ErrRoomFull；预留到期由 Tick 释放并报出房间与玩家，ttl 非正报 ErrInvalidTTL；未入座改准备报 ErrPlayerNotFound；不足两人或未全准备报 ErrNotEnoughPlayers、ErrNotAllReady；离开释放座位，房主离开由下一位接任；转移房主顺延回绕，接任者已掉线要继续往后找，无继任报 ErrNoSuccessor；开局瞬间的加入请求按房间状态拒绝，不能因有空位就放进；结算中的房间不收离开，查询保留到销毁；快照带席位与预留截止，重启后 Tick 按原基准到期，坏快照报 ErrBadSnapshot。

硬约束：并发加入同一座位仅一个赢家，其余收满员错误；写入与快照互斥，快照只见全量旧或新状态；房间数、席位数与预留表设硬顶。

验收：go test ./... 15 条全过，测试不得修改、跳过或写死；并发与重启自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-game-a-01`
- 实现文件：`room_lifecycle_guard.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`RoomLifecycleGuard`
