# 分层 LOD 集群构建：空间聚类、代理包围体与确定性节点号

## 题面

大世界切到分块 LOD 之后，远处山体整片消失，走近又突然冒出来；预渲染的远景代理体没有盖住边缘那几件，遮挡剔除干脆把整块地形判成空的，美术把资源摆好再增量补几棵树，整棵层级和一次加载出来的结果对不上，排查时连节点号都认不出来。落点 gsb/batch48-go-game-b-07 的 hlod_cluster_builder.go，类型 HLODClusterBuilder，签名保持：NewHLODClusterBuilder(cellSize float64) *HLODClusterBuilder、SetLimits(maxPerCluster, maxDepth int) error、SetCellSize(size float64) error、CellSize() float64、SetInstanceLimit(n int) error、Add(inst Instance) (NodeID, error)、Remove(instID InstanceID) error、Rebuild() (Tree, error)、NodeOf(instID InstanceID) (NodeID, bool)、NodeBounds(id NodeID) (Box, bool)、InstanceCount() int、Limits() (int, int)、WarningCount() int、Nodes() []Node、Snapshot() ([]byte, error)、Restore(payload []byte) error。Instance 由 ID 与 Bounds 组成，Box 用 Min 与 Max 两个 Vec3 表示，Node 带 ID、Level、Children、Instances 与 Bounds，Tree 带 Root、Depth、Leaves 与 Nodes，Nodes 按节点号升序。实例先按网格单元归拢，单元内超过簇上限就沿最长轴按中位数对半拆，一直拆到每簇不超上限或触到最大深度；Add 返回该实例当前所属叶簇号，实例表变动后要重新 Rebuild 才有完整层级，尚未构建时返回空串。

要修：同一实例号重复注册不报错，后一条悄悄覆盖前一条；空实例号、反向包围盒也照收；实例表没有上限，簇规模、最大深度或网格边长设成 0 也照跑；同一格里的三件东西被拆到三个叶簇，邻近实例根本没聚起来；簇内实例超过上限时不拆分，全堆在一个节点里；层级已经到最大深度还继续往下切，或者干脆把装不下的实例丢掉、告警计数恒为零；节点号跟着插入顺序走，同一份实例表连续两次重建出不同编号；删除实例后映射没清，NodeOf 还指向旧节点；代理包围体按中心点算，只部分落在聚类边界上的实例伸到盒外；增量注册几件再全量重建，节点号与层级跟一次建好时不一致；树里没有根节点信息，深度与叶簇数常年为零；快照只存实例表，恢复后 NodeOf 缺一半，损坏的恢复内容还会把现有实例表清掉一半。

硬约束：SetLimits 与 SetCellSize 收到零或负数直接报错，没设过簇规模与最大深度时 Rebuild 也报错而不是拿默认值硬撑；坐标完全相同的实例按下标对半拆，不能因为坐标相等就停止拆分；Add、Remove、Rebuild 在同一把锁下串行，并发注册不能多也不能丢，实例表受上限硬顶；节点号由簇内容确定，同一份实例表连续两次重建的节点号、层级与实例归属完全相同，Rebuild 交出的 Tree 与内部表分离，后续 Add 不能改到它；超过最大深度的簇按原样保留并计入告警，告警只统计触到深度上限仍超规模的簇，不允许静默丢实例；快照覆盖实例表、簇规模、最大深度与网格边长，恢复要么整体生效要么完全不生效，内容不完整一律拒绝并保留旧状态。

验收：go test ./... 的 15 条用例全过，不得改测试、跳过或按固定输入写死；并发注册与快照恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-game-b-07`
- 实现文件：`hlod_cluster_builder.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`HLODClusterBuilder`、`Instance`、`Box`、`Vec3`、`Node`、`Tree`
