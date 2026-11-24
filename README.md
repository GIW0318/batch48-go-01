# 半透明网格逐三角排序：视图相关重建、同深度稳定序与索引预算

## 题面

水面和玻璃改成逐三角排序以后，玩家绕场景走一圈能看到透明面片成片反转，相机小幅晃动时整块水面又突然重排一次，帧时间直接翻倍；同一条视线方向上重复重建本该复用上一帧顺序，实际却每次从头重排；美术换了一版顶点，旧的绘制顺序还挂在新网格上，画面里出现三角形拉丝。落点 gsb/batch48-go-game-b-09 的 triangle_sort_rebuilder.go，类型 TriangleSortRebuilder，签名保持：NewTriangleSortRebuilder(budget int) *TriangleSortRebuilder、SetIndexBudget(n int) error、IndexBudget() int、SetMesh(mesh Mesh) error、MeshVersion() uint64、Update(verts []Vertex) error、Rebuild(viewDir Vec3) (IndexBuffer, error)、LastOrder() (IndexBuffer, bool)、LastView() (Vec3, bool)、OrderStats() OrderStats、DegenerateCount() int、Reset() error、Snapshot() ([]byte, error)、Restore(payload []byte) error。Mesh 由 Vertices 与 Tris 组成，Tri 是三个顶点下标，IndexBuffer 存三角形号，OrderStats 带 Triangles、Degenerate、Sorted、Reused 与 Version。排序键是三角形质心与视线方向的点积，升序排列，点积相同按三角形号升序。

要修：空网格、三角形索引越界的网格被接受，顶点更新数量与网格不符也照收；没有网格就重建，视线方向是零向量也照排；索引预算设成 0 或负数，超过预算仍然重建并把索引截断；三角形按顶点顺序或三角形号排，而不是按质心沿视线的投影值；投影值相同时顺序随遍历漂移，同一份网格两次重建结果不同；索引缓冲没有覆盖每个三角形恰好一次，出现重复或遗漏；相机只挪一点就整体重排，时间相干复用完全没生效；退化三角形照样进索引，或者被跳过却没计数，索引重复和遗漏一起出现；相机穿过网格、深度关系整体反转时，复用上一帧顺序只做局部修正，排出来的前后次序颠倒；重建后索引没有交给调用方，LastOrder 永远是空的；顶点更新后网格版本变了，旧排序快照继续用于新版本；快照丢掉网格版本与视线方向，恢复后拿旧顺序配新网格，损坏内容还会把当前网格清掉。

硬约束：OrderStats 的 Version 记本次排序所用网格的版本、Sorted 记索引条数、Triangles 记网格三角形总数；Rebuild 与 SetMesh、Update 在同一把锁下串行，重建产出的是不可变索引快照，多个 goroutine 同时重建同一份网格必须得到同一顺序；视线方向与上一次完全一致时直接复用上一帧顺序并标记复用，发生大幅变化时整体重排；索引预算硬顶，按三角形条数计而不是按顶点数计，三角形数超限直接报错并保留上一条可用顺序，不允许截断；快照覆盖网格、网格版本、索引预算、视线方向与顺序，恢复要么整体生效要么完全不生效，内容不完整一律拒绝并保留旧状态。

验收：go test ./... 的 15 条用例全过，不得改测试、跳过或按固定输入写死；并发重建与快照恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-game-b-09`
- 实现文件：`triangle_sort_rebuilder.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`TriangleSortRebuilder`、`Mesh`、`Tri`、`Vertex`、`Vec3`、`IndexBuffer`、`OrderStats`
