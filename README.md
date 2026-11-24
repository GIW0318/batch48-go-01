# batch48-go-game-b-25

## 题面

关卡里的物件一多，视锥外的东西还在被提交：远处整片建筑被剔除后，玩家眼前的柱子偶尔整根消失；相机绕着物体转一圈，同一条网格时而可见时而不见，蹲下贴脸看墙时墙反而没了。策划把仓库区摆了两千个箱子，每帧查询要扫全表，主线程多花三毫秒；同一件道具跨着两个网格桶，候选列表里出现两次，阴影贴图被打了两遍。视锥平面随相机缩放变化时法线长度跟着变，同一份判定在不同分辨率下给出相反结果。编辑器里同一个相机矩阵在预览窗口与游戏窗口给出的可见集合不同，美术怀疑是矩阵约定不一致；场景里还有一批贴地的长条模型，包围盒横跨好几个格子，候选列表里反复出现，剔除统计的数字也对不上。每次切换相机都要重建整棵索引，切一次卡一次，场景大了以后切换成本无法接受。落点 gsb/batch48-go-game-b-25 的 camera_frustum_cull_index.go，类型 FrustumCullIndex，接口保持：NewFrustumCullIndex(cellSize float64) *FrustumCullIndex、SetCellSize(size float64) error、SetMaxResults(n int) error、SetMaxItems(n int) error、Build(items []Item) error、SetCamera(m Mat4) error、Query(layerMask uint32) ([]int, error)、Candidates(b Box) ([]int, error)、OrthoMatrix(l, r, btm, top, near, far float64) (Mat4, error)、Planes() ([]Plane, error)、Generation() uint64、ItemCount() int、CellCount() int、Resolve(id int) (Item, bool)、Stats() CullStats。Item 带 ID、Box、Sphere、Layer；Box 带 Min、Max；Sphere 带 Center、Radius；Plane 带 N、D，平面方程按 N 点乘 p 加 D 大于等于零为内侧。Planes 返回当前相机的六个归一化平面；Candidates 返回与查询盒相交的网格桶里的 ID，升序去重；Mat4 按行主序存储并与列向量相乘，新建索引的 Generation 为 0。

要修的行为：（1）Build 接受重复 ID、非正 ID、Min 大于 Max 的包围盒；（2）负半径的包围球照样入索引，超出容量上限也不报错；（3）ItemCount 与 Resolve 对不上，Resolve 查不到刚建入的物件；（4）OrthoMatrix 在左右、上下或近远跨度为零时也算出一个矩阵；（5）视锥平面没有归一化，法线长度随缩放变化，判定距离跟着漂；（6）Query 的结果没有按 ID 升序，也没有去重；层掩码被忽略，不匹配层的东西照样返回；（7）包围球或包围盒已经整体在外侧时仍被判可见，内侧的反而被剔掉；（8）包围盒刚好贴在视锥平面上的物件被剔除；（9）相机切换之后查询代数不递增，旧代数结果被继续复用；（10）没有设置相机时 Query 返回空列表而不是报错；（11）Candidates 不做网格查询，直接全表扫描，单次结果数没有上限；（12）一个包围盒横跨多个网格桶时被重复返回，去重键也算错；相机近平面落在物体内部时，该物体被当成完全在外而剔除。

硬约束：索引构建后相机切换不得重建索引，但查询代数必须递增且旧结果不得复用；单次候选数与索引容量是硬顶，超限返回可识别错误；判定必须保守，边界相接算可见。验收：go test ./... 15 条全过，不得修改测试、不得跳过、不得按固定输入写死；边界相接与近平面穿越自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-game-b-25`
- 包名：`task`
- 源码文件：`camera_frustum_cull_index.go`
- 测试目录：`tests/`
