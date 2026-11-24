# 骨骼 LOD 链构建：叶骨合并、末端保留与动画曲线迁移

## 题面

远处怪物切到低模后手臂动画被整段吞掉，末端的武器挂点和命中盒在简化后跑丢；同一关重复加载两次，简化出的骨骼 ID 与层级顺序还不一样，动画曲线迁到新层级上权重加起来不等于 1，角色一跑起来就抽。落点 gsb/batch48-go-game-b-19 的 bone_lod_chain_builder.go，类型 BoneLodChainBuilder，接口保持：NewBoneLodChainBuilder() *BoneLodChainBuilder、SetMaxLevels(n int) error、MaxLevels() int、BuildLod(base Skeleton, level, maxBones int) (Skeleton, error)、MapBone(level int, id string) (string, error)、LevelBones(level int) (Skeleton, bool)、MigrateCurves(src, dst Skeleton, tracks []Track) ([]Track, error)、Warnings() []string、Levels() int、Stats() BuilderStats、Reset()。Bone 带 ID、Parent、Weight、Terminal、Collider、Constraint；Skeleton 带 Root 与 Bones；Track 带 BoneID、Weight、Samples；BuilderStats 带 Levels、Builds、Warnings、Merged。

要修的行为：简化不按影响权重挑叶骨，先遍历到哪根就合哪根；末端骨与带碰撞体的骨被一起合掉，武器挂点和命中盒消失；根骨被当成叶骨合并，层级直接断掉；同权重叶骨的选择随内部遍历顺序变化，两次构建结果不同；MapBone 只在当前层找同 ID，已合并骨查不到最近祖先就报错；曲线迁移按原骨名硬匹配，已合并骨的整条轨道被丢掉；多条轨道落到同一个父骨时权重相加不等于 1，采样值也没按权重合并；级别与骨骼上限不校验，level 0、超过 maxLevels、maxBones 大于等于现有骨数都照做；构建失败后缓存里留着半级结果，未构建的级别也返回空结果而不是报错；BuildLod 与 MapBone 并发时读到只写了一半的层级和映射。

还有两条边界没有明确判据：被合并的骨上挂着约束时，是拒绝合并还是合并后记录告警；动画轨道接连引用多根已合并骨时，沿父链回退的先后顺序怎么定。

硬约束：BuildLod、MapBone、MigrateCurves、Warnings、Reset 在同一把锁下串行，同一份输入重复构建出的骨骼与映射逐字段一致；级别与骨数是硬上限，maxBones 必须小于源层级骨数，达不到就报 ErrCannotSimplify 且不落半级；曲线迁移后每个目标骨的权重和为 1，输出按 BoneID 升序，采样数组必须等长且非空。构建前校验 Root 非空且在 Bones 里、ID 唯一、根以外 Parent 存在且无环、Weight 非负，否则报 ErrBadSkeleton。合并只允许动叶骨，根骨与 Terminal、Collider 为真的骨不得合并；每次挑当前叶骨里权重最小的一根并到父骨，权重相同按 ID 升序，父骨权重累加被合骨的权重，直到骨数不大于 maxBones。level 小于 1 或超过 maxLevels 报 ErrBadLevel，maxBones 非正报 ErrBadMaxBones，SetMaxLevels 非正同样报 ErrBadLevel。每级映射要能反查任意基础骨或更低级别骨在该级的最近存活祖先，Levels 返回已构建的最高级别，LevelBones 对未构建的级别返回 false，级别未构建时 MapBone 报 ErrLevelNotBuilt，骨 ID 从未出现报 ErrUnknownBone。MigrateCurves 里目标层保留同 ID 的骨直接沿用，否则沿源骨骼父链逐级向上找最近一个存在于目标层的祖先，找不到报 ErrNoAncestor；权重为负、采样为空或长度不一致报 ErrBadTrack，某个目标组的权重和为 0 报 ErrZeroWeight；同组轨道合成一条，采样按原权重加权平均。

验收：go test ./... 的 15 条用例全部通过，不得改测试、跳过或按固定输入写死；并发构建与映射自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-game-b-19`
- 实现文件：`bone_lod_chain_builder.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`BoneLodChainBuilder`、`Bone`、`Skeleton`、`Track`、`BuilderStats`
