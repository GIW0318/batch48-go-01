# 房间到节点的装箱、预留与排空迁移

## 题面

大区维护前夜，排空后同一批房间被两个节点各记一次账，新房间又落进排空节点，容量报表少了三十多个座位。落点 gsb/batch48-go-game-a-05 的 room_placement_scheduler.go，类型 RoomPlacementScheduler，签名保持：NewRoomPlacementScheduler(maxNodes int) (*RoomPlacementScheduler, error)、Place(roomID string, need Seats, region string) (NodeID, error)、Reserve(nodeID NodeID, roomID string) error、Migrate(roomID string, toNode NodeID) error、Snapshot() ([]byte, error)、Restore([]byte) error，余下按注释保持签名。

要修：节点或区域为空、容量非正报 ErrInvalidNode、ErrInvalidRegion、ErrInvalidCapacity，重名报 ErrNodeExists；放置只在同区域选，无节点报 ErrNoNodeInRegion，容量不够报 ErrInsufficientCapacity，候选按剩余最紧装箱，同剩余按 ID 排序；房间为空、需求非正报 ErrInvalidRoom、ErrInvalidSeats，重复放置报 ErrRoomExists；释放归还容量，重复释放报 ErrPlacementNotFound；预留按席位在目标节点记账，重复预留报 ErrAlreadyReserved；迁移成功源释放目标占用、总量守恒，命中预留直接消耗；排空中节点不接新房间，但迁移队列房间仍占其剩余容量直到迁出；迁移失败房间回原节点并恢复容量账，不得两边各记；亲和与容量冲突按亲和优先并报可识别的容量不足；并发放置不超卖，最后座位只给一个请求；快照带节点与预留表，重启不重复占位，坏快照报 ErrBadSnapshot。

硬约束：放置、预留、释放、迁移共用一套裁决，账目守恒；写入与快照互斥；节点表与预留表设上限。

验收：go test ./... 15 条全过，测试不得修改、跳过或写死；并发与重启自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-game-a-05`
- 实现文件：`room_placement_scheduler.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`RoomPlacementScheduler`
