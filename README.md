# 蒙皮权重裁剪：Top-4 保留、归一化与全零回退根骨

## 题面

打完 Boss 之后角色肩甲上的尖刺一直抖，裙摆和腿互相穿插，法线也跟着乱；排查发现每个顶点挂了十几根影响骨，末尾几根权重只有千分之几却把真正起作用的骨挤出了上限，个别全零权重的顶点直接被世界原点吸走，网格拉出一条长条。落点 gsb/batch48-go-game-b-20 的 skin_weight_reducer.go，类型 SkinWeightReducer，接口保持：NewSkinWeightReducer(rootBone, maxInfluences int) *SkinWeightReducer、SetMaxInfluences(n int) error、SetMaxVertices(n int) error、MaxInfluences() int、MaxVertices() int、Reduce(in []Influence, max int) ([]Influence, error)、Normalize(in []Influence) ([]Influence, error)、Validate(v VertexSkin) error、Fallback(v VertexSkin) (VertexSkin, error)、ReduceMesh(m Mesh) (Mesh, error)、FallbackCount() int、RootBone() int、Stats() ReducerStats、Reset()。Influence 带 Bone 与 Weight；VertexSkin 带 Vertex 与 Influences；Mesh 带 Vertices 与 RootBone；ReducerStats 带 Reduced、Fallbacks、InfluencesIn、InfluencesOut；下限常量 MinWeight 取 1e-6。

要修的行为：裁剪按输入顺序取前几根，不按权重降序，弱的排在前面把强的顶掉；同权重不按骨索引升序，两次裁剪结果不一样；保留项之和没有归一到 1，直接把原权重输出；同一根骨出现多次时不合并，归一后总权重超过 1；全零或负权重顶点不回退根骨，要么返回空列表，要么把负权重原样留下；单顶点裁剪和整网格裁剪各写一套逻辑，同一顶点两种入口结果对不上；顶点数与每顶点影响数不设上限，超限时静默截断；Validate 不查重复骨、不查权重和、也不拦负权重；Fallback 不计数或重复计数；并发裁剪时 FallbackCount 与统计互相覆盖。

还有两条边界没有明确判据：第 max 名与第 max 加一名权重完全相同时保留哪一根，排序要稳定；归一化后个别权重落进次正规数甚至下溢成 0 时，下限怎么抬、抬完怎么重新归一。

硬约束：Reduce、Normalize、Validate、ReduceMesh、Fallback 与计数在同一把锁下串行，同一输入重复裁剪逐项一致；max 必须落在 1 到 MaxInfluences 之间，否则报 ErrBadMax，SetMaxInfluences 与 SetMaxVertices 收到非正数报 ErrBadLimit。裁剪规则是先按骨索引合并重复项并相加权重，丢弃非正权重和负骨索引项，再按权重降序、同权重骨索引升序排序，取前 max 项，除以保留项之和；归一后低于 MinWeight 的权重抬到 MinWeight 再整体重新归一。空输入或丢弃后没有任何正权重时，返回单条绑定姿势根骨、权重为 1，并把 FallbackCount 加一。Normalize 不裁剪，保留全部正权重项，同样合并、排序、归一；空输入或权重和为 0 报 ErrZeroWeight。Validate 要求 Vertex 与骨索引非负、权重非负、同一根骨不重复、条数不超过 MaxInfluences、权重和与 1 的偏差不超过 1e-6，分别报 ErrBadVertex、ErrNegativeWeight、ErrDuplicateBone、ErrTooManyInfluences、ErrEmptyInfluences、ErrSumMismatch。Fallback 返回根骨单条权重 1 并保留原 Vertex，Vertex 为负报 ErrBadVertex，被拒绝的调用不计数。ReduceMesh 在顶点数超过 MaxVertices 时整份报 ErrTooManyVertices 而不是截断；每个顶点的结果必须与单独调用 Reduce 完全一致，输出顺序与 Vertex 原样保留。

验收：go test ./... 的 15 条用例全部通过，不得改测试、跳过或按固定输入写死；并发裁剪与计数自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-game-b-20`
- 实现文件：`skin_weight_reducer.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`SkinWeightReducer`、`Influence`、`VertexSkin`、`Mesh`、`ReducerStats`
