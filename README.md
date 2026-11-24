# 任务包 batch48-go-game-a-16

## 模块

- module: gsb/batch48-go-game-a-16
- package: task
- file: interest_grid_broker.go

## 题面

开放世界分服上线两周，外围玩家集中反馈三类怪现象：只在营地边缘来回走两步，视野里的 NPC 和地面掉落就整批重进重出，小地图不停闪；有人用传送卷轴跨越大半张地图后，网关在一瞬间吐出上千条进入消息，客户端卡死两秒；还有玩家切图掉线时，同房间其他人跟着丢了一帧差分，几个实体在别人屏幕上永久消失。落点 gsb/batch48-go-game-a-16 的 interest_grid_broker.go，类型 InterestGridBroker，签名保持：NewInterestGridBroker(cellSize float64) *InterestGridBroker、CellOf(pos Vec2) Cell、RegisterClient(clientID string, pos Vec2) error、RemoveClient(clientID string) error、RegisterEntity(entityID EntityID, pos Vec2) error、MoveViewer(clientID string, pos Vec2) error、MoveEntity(entityID EntityID, pos Vec2) error、Diff(clientID string) (Delta, error)、SetRadius(enter, leave float64) error、SetCellLimit(n int) error、SubscribedCells(clientID string) ([]Cell, error)、Stats() BrokerStats、Snapshot() ([]byte, error)、Restore(payload []byte) error。

要修：格子归属按 floor(pos/cellSize) 计算，负坐标在边长 10 时 -5 归 -1 格，不能截断到 0；客户端首次 Diff 把当前可见实体一次性列为进入，此前已交付集合为空；观察点跨格移动后，进入半径内的实体列为进入，超出离开半径的实体列为离开；同一次差分里同一实体不能既进入又离开，三组分别按实体编号升序输出，Diff 交付后立即清空增量，紧接着再取一次三组均为空；同一 tick 内实体多次移动只产生一条属性更新，位置取最终值；每客户端订阅格数受上限约束，超限按切比雪夫距离从远到近裁剪，同距按 X、Y 升序保留，裁剪总数累加进 Stats 的 TrimmedCells；SetRadius 拒绝零或负的进入半径以及离开半径不大于进入半径的输入，SetCellLimit 拒绝小于 1 的配额；未注册实体移动返回 ErrEntityUnknown，重复注册返回 ErrEntityExists，未注册客户端取差分返回 ErrClientUnknown；实体在进入与离开半径之间的滞回带内反复移动不产生进入或离开事件，但位置变化仍要以属性更新发给已订阅客户端；观察点一次移动跨过两倍以上网格尺寸时按整体重算处理，不能沿路逐格产生进入或离开；一个 tick 处理中途注销客户端时，其未消费的差分直接丢弃，其他客户端的差分生成不受影响；快照保存方格边长、实体位置、客户端订阅与已交付集合，Restore 对空载荷或损坏载荷返回 ErrBadSnapshot，恢复后再次 Diff 不重复交付。默认进入半径 1 格、离开半径 2 格、订阅上限 64 格。

硬约束：RegisterEntity、MoveEntity 与各客户端 Diff 可并发调用，实体索引与已交付集合的推进必须在同一临界区内完成，并发移动同一实体后差分只反映唯一最终位置；订阅上限硬性生效，任何路径都不得让单客户端订阅格数越过上限；快照必须自包含，恢复后不依赖旧实例内存。

验收：go test ./... 15 条全过，不得改测试、跳过或按固定输入写死；并发与重启恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```
