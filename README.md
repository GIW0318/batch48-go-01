# batch48-go-game-b-03

## 题面

换季版本加了大量植被材质之后，画面开始出问题：半透明的树叶被塞进写深度的不透明通道，远处整片树冠变成黑块，靠近才慢慢透出来；阴影贴图里出现了明确标了不接收阴影的道具，另一些真正挡光的物体却完全没有投影，负责阴影的同学发现代码拿接收方标志当成了投射标志。变体缓存也在打架：同一个材质同时进阴影和主通道，两次写进的缓存键一模一样，后写的把先写的覆盖掉，切换画质时整批材质重新编译。

范围是 gsb/batch48-go-game-b-03 下的 material_pass_router.go，类型 MaterialPassRouter，负责材质到渲染通道的路由、变体键生成、通道分配、每通道预算与变体缓存失效。

公开接口保持：`NewMaterialPassRouter() *MaterialPassRouter`、`Route(mat Material) ([]PassID, error)`、`VariantKey(mat Material, pass PassID) (string, error)`、`Assign(pass PassID, matID string) error`、`PassBudget(pass PassID, limit int) error`、`Invalidate(matID string) error`、`Materials(pass PassID) []string`、`CacheEntries() int`、`Reset() error`、`Snapshot() ([]byte, error)`、`Restore(data []byte) error`。Material 带 ID、Blend、DoubleSided、DepthWrite、CastShadow、ReceivesShadow 与 SortKey；BlendMode 取 opaque、masked、alpha、additive；PassID 取 shadow、opaque、transparent。

要修：一、材质号为空报 ErrEmptyMaterialID，混合模式不在四种之内报 ErrUnknownBlend；二、不透明且写深度的材质只进 opaque，不透明但不写深度的进 transparent，masked 写深度只进 opaque，alpha 与 additive 只进 transparent，双面标志不改变通道；三、投射阴影的材质先在路由前插入 shadow，再按混合模式接主通道，顺序固定；四、半透明材质不得进入写深度的不透明通道，运行期从半透明改回不透明要重新路由；五、阴影通道只接受投射阴影的材质，接收方标志不能替代投射标志；六、同一材质在同一通道只出现一次，重复 Route 必须给出同一路由，重复 Assign 不重复计入；七、变体键形状固定为 `opaque|opaque|ds=0|dw=1|cs=0` 这样的通道、混合模式与三个布尔标志拼接，同一材质在两个通道生成的键必须不同，否则变体缓存互相覆盖；八、VariantKey 对未知通道报 ErrUnknownPass、对未路由材质报 ErrUnknownMaterial、对不在路由里的通道报 ErrPassNotRouted；九、Assign 未知材质报 ErrUnknownMaterial、外来通道报 ErrPassNotRouted、未知通道报 ErrUnknownPass，重复 Assign 幂等；十、每通道默认预算 MaxMaterialsPerPass，PassBudget 只收正数否则报 ErrBadBudget，未知通道报 ErrUnknownPass，超预算 Assign 报 ErrBudgetExceeded 且不写入；十一、Materials 按 SortKey 升序、同键按材质号升序，结果可复现，Invalidate 只失效该材质的变体，未知材质报 ErrUnknownMaterial，重建后键与原键一致；十二、快照覆盖路由、通道分配、预算与变体缓存键，恢复要么整体生效要么完全不生效，坏载荷报 ErrBadSnapshot，Reset 清空全部路由、分配与缓存。

硬约束：Route、VariantKey、Assign、Invalidate、Snapshot 与 Restore 在同一把锁下串行，四路并发 Assign 同一通道时每个材质只能出现一次，预算判定与写入必须原子，超预算不能留下半个分配。变体缓存按材质粒度失效，禁止整表清空；每通道材质数与缓存条目数都是硬顶，超限报错而不是淘汰已分配材质。快照恢复后路由、分配顺序与预算都要在，且不与旧对象共享可变状态。

验收：`go test ./...` 15 条全部通过；不得修改测试、跳过或按固定输入写死；并发与重启恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 平台字段

- name: batch48-go-game-b-03
- domain: 游戏、图形与多媒体
- sub: 图形渲染
- type: Bugfix
- difficulty: 困难
- module: material_pass_router
- typeName: MaterialPassRouter
- file: material_pass_router.go
