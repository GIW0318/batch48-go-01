# batch48-go-game-b-04

## 题面

把阴影换成四级级联之后，玩家跑图时地面出现一圈圈跳动的明暗接缝，站在交界处不动都会闪；骑马从远处接近城堡，屋檐上的阴影每隔几帧就在两级之间来回切一次，靠近之后才稳定。显存紧张把槽位砍到两个之后更糟：整个阴影直接黑屏，近处物体一点投影都没有；另一台机器调整了最远覆盖距离，超出范围的山体没有变暗，反而整体套上了第一级的高精度阴影。美术还反馈级联数从四级改到五级之后，第一帧有几片地面用的是旧编号的采样，闪了一帧才恢复。

范围是 gsb/batch48-go-game-b-04 下的 cascade_shadow_slotter.go，类型 CascadeShadowSlotter，负责相机范围到级联的划分、级联到阴影槽位的映射、分辨率推导、切档滞回与快照恢复。

公开接口保持：`NewCascadeShadowSlotter() *CascadeShadowSlotter`、`Split(cam Camera, cascades int) ([]Cascade, error)`、`AssignSlots(cascades []Cascade, slots int) error`、`Resolution(cascadeID int) (int, int, error)`、`SetCamera(cam Camera) error`、`CommitFrame() error`、`Camera() (Camera, error)`、`Hysteresis(enter, exit float64) error`、`SelectCascade(distance float64) (int, error)`、`Slots() []Cascade`、`Snapshot() ([]byte, error)`、`Restore(data []byte) error`。Camera 带 Near、Far、FovY 与 Aspect；Cascade 带 ID、Near、Far、Slot、Width 与 Height；常量 MaxCascades、MaxSlots、BaseResolution 与 MinResolution。

要修：一、相机 Near 必须大于零、Far 必须大于 Near、FovY 在零到一百八十之间、Aspect 大于零，否则报 ErrBadCamera；二、级联数取 1 到 MaxCascades，否则报 ErrBadCascadeCount，划分按恒定比值，首级 Near 与相机 Near 相同，末级 Far 与相机 Far 相同，级与级首尾相接不留缝；三、AssignSlots 的槽位数取 1 到 MaxSlots，否则报 ErrBadSlotCount，级联表为空或超过 MaxCascades 报 ErrBadCascadeCount，槽位从零开始升序编号；四、槽位不足时按级联顺序把放得下的放进槽位，放不下的标记为负一，不得整体失败；五、Resolution 从 BaseResolution 起每级减半，低于 MinResolution 时取 MinResolution，未知或负数级联号报 ErrNoSuchCascade；六、未配置相机或级联表时，Camera、Resolution 与 SelectCascade 报 ErrNotConfigured；七、SetCamera 只做暂存，CommitFrame 才在帧边界生效，边界之前并发读取只能看到完整的旧配置或完整的新配置，不能读到半套；八、Hysteresis 要求 enter 大于 exit 且 exit 大于零，否则报 ErrBadHysteresis，阈值内的距离抖动不得引起级联切换；九、级联数变化时槽位重新编号，引用旧编号的采样必须在同一帧内失效；十、最远级联的覆盖距离变化后，超出覆盖范围的物体不得回落到第一级阴影；十一、SelectCascade 对负距离报 ErrBadDistance，落在某个级联区间时返回该级编号；十二、快照覆盖级联表、相机、槽位与滞回阈值，恢复要么整体生效要么完全不生效，坏载荷报 ErrBadSnapshot 并保留旧表。

硬约束：Split、AssignSlots、SetCamera、CommitFrame 与 Snapshot 在同一把锁下串行，八路并发读取期间写入只能整帧切换，读到的相机与分辨率必须来自同一套配置；级联数变化与槽位重编号在帧边界一次完成，不能在帧内出现新旧编号混用。级联数、槽位数与分辨率下限都是硬顶，超限报对应错误而不是截断；快照恢复后级联表、槽位分配、相机与滞回阈值都要在，且不与旧对象共享可变状态。

验收：`go test ./...` 15 条全部通过；不得修改测试、跳过或按固定输入写死；并发与重启恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 平台字段

- name: batch48-go-game-b-04
- domain: 游戏、图形与多媒体
- sub: 图形渲染
- type: Bugfix
- difficulty: 困难
- module: cascade_shadow_slotter
- typeName: CascadeShadowSlotter
- file: cascade_shadow_slotter.go
