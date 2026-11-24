# 骨骼重定向：语义骨名映射、静止姿势差与缺失骨回退

## 题面

换了个体型更壮的主角之后，跑步动画的手臂穿进胸腔，头骨在转身时被拉长，同一个动作每次重定向出来的姿势还略有不同；把动画套到带披风辅助骨的怪物身上，披风骨全部缩成单位变换，跟着身体一起僵在原地。落点 gsb/batch48-go-game-b-18 的 skeleton_retarget_mapper.go，类型 SkeletonRetargeter，接口保持：NewSkeletonRetargeter(maxMappings int) *SkeletonRetargeter、SetMaxMappings(n int) error、MaxMappings() int、BuildMap(src, dst Skeleton) (*BoneMap, error)、Map() (*BoneMap, bool)、Retarget(p Pose) (Pose, error)、SetRestDelta(v Vec3) error、RestDelta() Vec3、Missing(name string) (Fallback, bool)、Mappings() []Mapping、Conflicts() []Conflict、Normalize(name string) string、Reset()。Vec3 带 X、Y、Z；Bone 带 Name、Parent、Role、Length、RestPos、RestRot、RestScale；Skeleton 带 Root 与 Bones；Pose 带 Rot、Pos、Scale 与 Fallback；Mapping 带 Source、Target、MatchedBy，Conflict 带 Source、Targets，Fallback 带 Source、Strategy，BoneMap 带 Source、Target、Pairs、Conflicts、Missing。

要修的行为：映射只认名字且区分大小写，语义角色相同的骨因为改名、前缀不同匹配不上；一个源骨按角色命中多个目标骨时静默取第一个，未登记冲突；缺失骨不登记，Missing 永远返回 false；目标骨骼里多出来的辅助骨没有源对应时直接给单位旋转和原缩放，不沿父链继承；旋转直接抄源姿势，没减去源静止姿势再加目标静止姿势与 RestDelta；缩放不按目标骨长与源骨长比例换算；位移不按根骨长度比例换算；SetRestDelta 不做范围与有限性校验，NaN 和超大角度照收；同输入两次 Retarget 结果不一致，Mappings 顺序随内部映射遍历变化；映射表不封顶，BuildMap 与 Retarget 并发时还能读到半张表。

还有两条边界没有明确判据：目标骨骼多出辅助骨、辅助骨自己又带子骨时，父子链要一路往上推导到最近一个可映射祖先；左右对称骨名规范化后落到同一个键时，是登记冲突还是让后一个覆盖前一个。

硬约束：BuildMap、Retarget、Mappings、Missing、Reset 在同一把读写锁下串行，BuildMap 失败时上一张完整映射表必须原样保留；映射条数是硬上限，超过就报 ErrMappingCapacity 且不落半张表；同一份骨骼与姿势重复重定向，每个分量与 Fallback 列表逐项一致，冲突与映射输出按源骨名升序。骨骼校验要求 Root 非空且在 Bones 里、骨名唯一、根以外 Parent 存在且无环、Length 大于 0、RestScale 每个分量大于 0，否则报 ErrBadSkeleton。匹配顺序是先看非空且相等的 Role，唯一命中记 role，多个命中记一条 Conflict 且该源骨不产生映射；Role 没命中再按规范化名字找未被占用的目标，唯一命中记 name，多个命中同样记冲突；仍然没有就写入 Missing，父链上已有映射骨的 Strategy 记 parent，否则记 root，同一个目标骨最多被一个源骨占用。Normalize 统一小写、去掉首尾空格、把点号冒号与空格折成下划线、合并连续下划线、去掉重复的 bip01_ 与 mixamorig_ 前缀，并去掉结尾的 _l、_r 左右后缀。Retarget 的旋转等于目标静止旋转加源姿势相对源静止的差再加 RestDelta；缩放按源静止缩放取相对量后乘目标骨长与源骨长之比；位移按源静止位置取差后乘目标根骨与源根骨的长度之比，姿势里缺失的通道按静止值处理。没有源映射的目标骨沿父链复制最近一个已映射目标的旋转、位移与缩放，父链上都没有就用它自己的静止姿势，并把骨名按升序放进 Fallback，绝不静默给单位变换。RestDelta 每个分量必须有限且绝对值不超过 180，否则报 ErrBadRestDelta。Reset 清空映射、冲突、缺失登记与 RestDelta。

验收：go test ./... 的 15 条用例全部通过，不得改测试、跳过或按固定输入写死；并发重定向自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-game-b-18`
- 实现文件：`skeleton_retarget_mapper.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`SkeletonRetargeter`、`Vec3`、`Bone`、`Skeleton`、`Pose`、`BoneMap`、`Mapping`、`Conflict`、`Fallback`
