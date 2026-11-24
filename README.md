# batch48-go-game-b-01

## 题面

大世界切到室内场景后，相机一推近，整片墙面和地板就消失了；贴着门框移动时，一半在近裁剪面后面的三角形被整块丢掉，屏幕上留下一条撕裂的亮边，裁剪缝两侧的法线和贴图坐标对不上，顶点色也突然变深。夜班还遇到两个更难查的：每帧新生成的顶点没有进预算，长镜头跑十几分钟顶点数缓慢上涨，最后报显存不足；同一份网格被渲染线程和工具线程同时裁剪，两边共用同一块暂存缓冲，输出的索引指到了别的三角形上，偶尔还越界。

范围是 gsb/batch48-go-game-b-01 下的 near_plane_clipper.go，类型 NearPlaneClipper，负责跨近裁剪面的三角形与网格裁剪、新顶点属性插值、退化面剔除、索引重编号、顶点预算与快照恢复。

公开接口保持：`NewNearPlaneClipper(limits Limits) *NearPlaneClipper`、`ClipTriangle(t Triangle, plane Plane) ([]Triangle, error)`、`ClipMesh(m *Mesh, plane Plane) (*Mesh, error)`、`ClipCount() int`、`RemapIndex(old []uint32) ([]uint32, error)`、`Budget(limit int) error`、`SignedDistance(v Vec3, plane Plane) float64`、`PlaneFromPointNormal(point, normal Vec3) Plane`、`Area(t Triangle) float64`、`Stats() Stats`、`Reset() error`、`Snapshot() ([]byte, error)`、`Restore(data []byte) error`。Vec3 带 Add、Sub、Scale、Dot、Cross、Lerp；Vertex 带 Position、Normal、UV、Color、Bones 与 Weights；Plane 是 Normal·P + Offset；Mesh 由 Vertices 与 Indices 组成。

要修：一、完全在平面正面的三角形原样返回，不新建顶点也不改动计数；二、跨面三角形按交点拆成扇形，交点处补出的新顶点正好落在交线上，扇形顺序固定；三、位置、法线、UV 与顶点色按参数插值，权重随骨骼索引一起保留，整数型骨骼索引不得线性插值；四、三点重合或三点共线的输入报 ErrDegenerateTriangle，计数与统计保持原值；五、裁剪后面积为零的碎片安静丢弃，不得报错；六、同一次裁剪里重复出现的交点要合并，否则顶点预算会被同一处缝隙反复吃掉；七、网格裁剪超过顶点预算时整体失败报 ErrBudgetExceeded，不返回半个网格也不消耗计数；八、成功时输出索引重新编号且密集，每个输出顶点都被引用；九、空网格返回空网格，nil 网格报 ErrNilMesh，法线为零报 ErrBadPlane，索引越界报 ErrIndexOutOfRange；十、任何一个三角形非法都不能让实例停在半成功状态；十一、RemapIndex 按首次出现顺序压缩，重复索引映射到同一号，空输入返回空结果，超过索引上限报 ErrIndexLimit；十二、Budget 只接受正数，零与负数报 ErrBadBudget。

硬约束：ClipTriangle、ClipMesh、Snapshot 与 Restore 在同一把锁下串行，每次调用使用自己的暂存缓冲，八路并发裁剪互不污染且计数等于各次新增之和；预算判定与计数落账必须原子，超预算时既不留下半个网格，也不提前记账。快照要覆盖生成数、输出数、丢弃数与当前预算，恢复要么整体生效要么完全不生效，载荷残缺或版本不符报 ErrBadSnapshot 并保留旧状态；顶点数与索引数按 MaxVerticesPerMesh 与 MaxIndicesPerMesh 硬顶，新增顶点预算自 DefaultVertexBudget 起算，裁剪结果不得突破任何一项。

验收：`go test ./...` 15 条全部通过；不得修改测试、跳过或按固定输入写死；并发与重启恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 平台字段

- name: batch48-go-game-b-01
- domain: 游戏、图形与多媒体
- sub: 图形渲染
- type: Bugfix
- difficulty: 困难
- module: near_plane_clipper
- typeName: NearPlaneClipper
- file: near_plane_clipper.go
