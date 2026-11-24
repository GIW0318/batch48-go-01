# 材质纹理槽绑定：同纹理复用、采样器冲突与槽位耗尽回退

## 题面

美术把一批新材质接进场景后，编辑器里预览正常，进游戏却看到木箱和铁桶共用同一张法线贴图，换了 wrap 的布料边缘没有各向异性效果；同一材质反复打开关闭后物理槽只增不减，材质热重载之后旧贴图还残留在新绘制里，两张卡的槽位统计对不上。落点 gsb/batch48-go-game-b-16 的 material_texture_slot_binder.go，类型 TextureSlotBinder，接口保持：NewTextureSlotBinder(defaultTexture string, slotLimit int) *TextureSlotBinder、SetSlotLimit(n int) error、SetMaterialLimit(n int) error、Bind(materialID string, s Slot) (int, error)、Unbind(materialID string, slot int) error、Resolve(materialID string) ([]BoundSlot, error)、ResolveAll() map[string][]BoundSlot、SlotCount() int、RefCount(slot int) int、Degraded(materialID string) bool、ReloadMaterial(materialID string) error、Generation(materialID string) uint64、EndFrame()、Stats() BinderStats。Sampler 带 WrapU、WrapV、Filter、Anisotropy，Wrap 只接受 repeat、mirror、clamp，Filter 只接受 nearest、linear、anisotropic，Anisotropy 取 1 到 16；Slot 带 Name、Texture、Sampler；BoundSlot 带 Name、Texture、Sampler、Slot、Degraded、Generation；BinderStats 带 PhysicalSlots、LiveBindings、PendingReclaim、DegradedMaterials。

要修的行为：材质 ID、槽名、纹理名为空仍被接受，wrap 拼错、filter 写错、各向异性为 0 也照收；同纹理同采样器的两个材质各占一个物理槽，引用计数没有用起来，SlotCount 与 RefCount 对不上；同纹理但 wrap 或 filter 不同被并成一槽，后绑定的采样状态覆盖了先绑定的；同一材质同一个槽名重复绑定不幂等，同纹理同采样器每调一次就多一条记录并涨引用计数，换纹理或换采样器则静默覆盖，而不是报同名冲突；解绑不存在的槽位照样成功，引用计数被减成负数；Resolve 不按物理槽升序，同一材质两次调用顺序不一致；物理槽用尽后新材质直接报错并丢掉采样状态，没有回退默认纹理，也没有降级标记；把槽位上限调低到低于当前存活槽数仍然生效，已绑定材质被挤掉；单材质可绑槽数不封顶，一个材质能占满全部物理槽；热重载只换纹理名，旧绑定、旧引用计数和降级标记全留在表里。

还有两条边界没有明确判据：各向异性只差一档，例如 4 和 5 的两个采样器，是否算同一个状态、能不能合并同槽；热重载丢掉但上一帧绘制列表还引用着的旧槽位，什么时候真正回收。

硬约束：绑定、解绑、热重载、统计在同一把锁下串行，任一时刻 RefCount 不得为负，并发结束后 LiveBindings 必须等于各物理槽引用计数之和；物理槽上限与单材质槽上限都是硬顶，超限只把该材质降级到默认纹理，不截断已有绑定、不丢采样器；同名同纹理同采样器的重复绑定按幂等处理，返回原槽位且不增加引用计数。物理槽从 0 开始按当前空闲且不在待回收列表里的最小下标分配；Stats 的 PhysicalSlots 是仍有引用的物理槽数，LiveBindings 是绑定条数，PendingReclaim 是待回收槽数，DegradedMaterials 是存在降级项的材料数。未绑定过的材质 Resolve 返回空列表且不报错；槽位耗尽时 Bind 返回 -1 并以默认纹理登记降级项，Resolve 里仍带原采样器；每次 ReloadMaterial 让 Generation 加一，未知材质重载也成功。

验收：go test ./... 的 15 条用例全部通过，不得改测试、跳过或按固定输入写死；并发绑定解绑、热重载后计数自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-game-b-16`
- 实现文件：`material_texture_slot_binder.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`TextureSlotBinder`、`Sampler`、`Slot`、`BoundSlot`、`BinderStats`
