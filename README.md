# batch48-go-game-b-11

## 题面

美术同学在新地图上挪一盏探照灯，整片场景的光照索引就全部重算一遍，帧率从九十掉到三十；更怪的是，灯只在两个格子交界处来回抖动半米，明明画面没有变化，远处的静态灯却跟着闪。程序化生成的灯阵里同一盏灯偶尔被加进两次，集群里的灯号出现重复。灯被删除以后，它原来占用的编号还残留在索引里，下一帧取到的列表里能看到已经不存在的光源。上周把集群容量调小做压力测试，容量满之后新灯加不进去，但已经登记的老灯在相邻集群里也一起消失了，整块区域直接黑掉。重启服务器后从存档恢复，灯的位置对得上，可每个集群的代际计数全部回到零，增量更新以为拿到的是最新数据，脏标记不再触发。

请修 gsb/batch48-go-game-b-11 模块里的 light_cluster_dirty_tracker.go，类型名为 LightClusterTracker，包名 task，只用标准库。接口保持现状：NewLightClusterTracker(TrackerConfig) *LightClusterTracker、AddLight(Light) (int, error)、MoveLight(int, Vec3, float64) error、RemoveLight(int) error、ClusterAt(ClusterKey) ([]int, uint64)、ClusterOf(Vec3) (ClusterKey, error)、Rebuild() error、SetDirtyBudget(int) error、ClearDirty() error、DirtyClusters() []ClusterKey、Generation(ClusterKey) uint64、Stats() TrackerStats、Snapshot() ([]byte, error)、Restore([]byte) error、Reset() error。Light 含 ID、Center、Radius；Vec3 含 X、Y、Z；ClusterKey 含 X、Y、Z；TrackerConfig 含 CellSize、ClusterCapacity、DirtyBudget；TrackerStats 含 Lights、Clusters、Dirty、Rebuilds、Degraded、Generation。

需要修的行为：

一、AddLight 的 Light.ID 大于零时按给定编号登记，等于零时分配当前最小可用正整数；编号已存在返回 ErrLightExists，半径小于等于零返回 ErrBadRadius；任意覆盖集群达到 ClusterCapacity 时返回 ErrClusterFull，已有灯的索引不受影响。

二、光源按轴对齐包围盒覆盖的每个集群登记，同一集群内的编号去重并升序返回，ClusterAt 的代际随该集群变更递增。

三、MoveLight 只重算旧包围盒与新包围盒覆盖到的集群，其余集群的编号列表与代际原样保留。

四、移动前后的集群集合取并集，同一集群在一帧内只重算一次；DirtyClusters 返回自上次 ClearDirty 以来标脏的集群，按 X、Y、Z 升序且不重复。

五、RemoveLight 把编号从它覆盖的所有集群里删除，未知编号返回 ErrLightMissing。

六、单次操作影响的集群数超过 SetDirtyBudget 设定的上限时，退化为整体重建，Rebuilds 加一，Degraded 置位，所有已知集群代际前进。

七、Rebuild 无条件重建全部索引并推进代际，SetDirtyBudget 拒绝小于等于零的预算并返回 ErrBadBudget。

八、ClusterOf 用 CellSize 做向下取整，负坐标同样向下取整，配置的 CellSize 小于等于零时返回 ErrBadCellSize。

九、Stats 的 Lights、Clusters、Dirty 必须和 ClusterAt、DirtyClusters 的实际结果一致，不能是估算值。

十、Snapshot 序列化灯表、集群索引和代际，Restore 要么整体成功要么保持原状态，坏数据返回 ErrBadSnapshot。

十一、Reset 清空所有灯与脏标记，代际与重建计数一并归零。

十二、同一盏灯在同一集群只允许出现一次，重复登记或跨界移动都不得产生重复编号。

并发与原子性：移动、删除与查询必须能同时进行，查询只能看到移动前的完整一版或移动后的完整一版，不能看到编号列表与代际不匹配的中间态。重启恢复：Restore 之后代际必须延续而不是从零开始，否则增量更新会误判。资源上限：脏集群预算与集群容量都要生效，超限走整体重建而不是无限增长。

验收方式：go test ./... 的 15 条用例必须全部通过，不得修改用例、跳过或用固定输入写死结果；并发与重启需要自行验证；只允许本地实现，不引入第三方依赖。

## 模块与运行

- 模块路径：`gsb/batch48-go-game-b-11`
- 包名：`task`
- 实现文件：`light_cluster_dirty_tracker.go`
- 测试目录：`tests/`
- 运行全部用例：`go test ./...`
