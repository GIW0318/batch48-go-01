# batch48-go-game-b-02

## 题面

大世界改成按空间分块维护可见集合之后，玩家站在区块交界的桥上，桥面时隐时现：对象从一个单元挪到隔壁单元，订阅方只收到新增收不到移除，旧模型在场景里叠了一排残影；值班美术把可见类别过滤从建筑切到植被，已经在那里的树没有出现，只有新进视野的树才冒出来，重进场景才恢复正常。断线重连的问题更麻烦：客户端拿到的差异比实际多一大截，已经确认过的对象又被下发一遍，版本号掉回旧值，订阅方反复回执却始终追不上。

范围是 gsb/batch48-go-game-b-02 下的 visible_set_incremental.go，类型 VisibleSetIndex，负责空间单元的可见对象集合、对象类别掩码、按订阅方过滤的增量差异与确认水位。

公开接口保持：`NewVisibleSetIndex(limits Limits) *VisibleSetIndex`、`Subscribe(subID string, cells []string) error`、`Unsubscribe(subID string) error`、`Bind(cellID string, objects []ObjectID) error`、`Classify(objID ObjectID, mask uint64) error`、`Mask(objID ObjectID) (uint64, bool)`、`Move(objID ObjectID, fromCell, toCell string) error`、`SetFilter(subID string, mask uint64) (uint64, error)`、`Diff(subID string) (added, removed []ObjectID, err error)`、`Ack(subID string, version uint64) error`、`Version(subID string) uint64`、`Cells() []string`、`Objects(cellID string) []ObjectID`、`Subscriber(subID string) (SubState, bool)`、`Snapshot() ([]byte, error)`、`Restore(data []byte) error`。Limits 给出 MaxCells、MaxSubs 与 MaxObjectsPerCell；SubState 带 ID、Cells、Filter 与 Version。

要修：一、单元号与订阅号必须非空，订阅的单元表为空报 ErrBadSub，引用了未绑定的单元报 ErrNoSuchCell，重复订阅报 ErrDuplicateSub；二、单元对象表按升序去重后整体替换，绑定失败不得改动原表，单元数、订阅数与单单元对象数按 Limits 硬顶，分别报 ErrCellLimit、ErrSubLimit 与 ErrObjectLimit；三、对象在单元间移动要同时从源单元移除并加入目标单元，源单元没有该对象报 ErrNotInCell，未知对象报 ErrNoSuchObject，目标单元未绑定报 ErrNoSuchCell，目标已满报 ErrObjectLimit，同单元自移是空操作；四、订阅初始为全位掩码，SetFilter 之后必须按新掩码重算基线，而不是只对之后的增量套用新掩码，重复设置同一掩码不推进版本；五、差异按订阅方已确认的基线计算，新增与移除各自升序，未确认前差异持续累积不清空，Ack 旧版本报 ErrStaleVersion；六、同一对象在一次差异中不得同时出现在新增与移除，跨单元移动也只能上报一次；七、对象在单元间移动时，既没订阅旧单元也没订阅新单元的订阅方，不得收到该对象的任何差异；八、Diff、Ack、Unsubscribe 与 SetFilter 对未知订阅一律报 ErrNoSuchSub；九、Classify 保存对象类别掩码，置零是合法操作，Mask 对从未分类的对象返回假；十、Cells 与 Objects 按升序返回，未绑定单元列出空表；十一、快照覆盖单元、对象类别、订阅、过滤掩码与确认基线，恢复后差异从基线继续，版本水位不回退；十二、快照载荷残缺或版本不符报 ErrBadSnapshot，且必须保留原有索引。

硬约束：Subscribe、Bind、Move、Diff 与 Ack 在同一把锁下串行，四路并发绑定不同单元后各自的差异必须完整且互不串扰，同一时刻两次 Diff 对同一订阅返回一致结果；改动只累加一次版本，脏单元在同一版本内只重算一次差异，禁止每读一次就重扫全表。快照要带上过滤掩码、确认基线与版本号，恢复要么整体生效要么完全不生效，被拒绝的载荷不得留下半个索引；单元数、订阅数与单单元对象数都是硬顶，超限报对应错误而不是覆盖或截断。

验收：`go test ./...` 15 条全部通过；不得修改测试、跳过或按固定输入写死；并发与重启恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 平台字段

- name: batch48-go-game-b-02
- domain: 游戏、图形与多媒体
- sub: 图形渲染
- type: Bugfix
- difficulty: 困难
- module: visible_set_incremental
- typeName: VisibleSetIndex
- file: visible_set_incremental.go
