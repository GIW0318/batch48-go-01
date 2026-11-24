# batch48-go-game-b-21

## 题面

战斗特效在密集交火时会卡住半秒，粒子一多，爆炸拖尾的颜色就串到相邻的烟尘材质上；上一波爆炸的残留还会跟着新一波火焰一起消失，可用的实例槽位越跑越少，跑图半小时后新发射的粒子干脆不显示。美术给爆炸、火星、烟尘分别配了不同的混合模式与图集页，程序化生成时本来就该按材质归成寥寥几个子批次，现在同屏两万粒子却提交了几十次绘制，抓帧看到每个批次只剩十几个粒子；策划临时把一轮齐射的粒子量翻倍，帧率直接从六十掉到二十出头。外包团队换了图集打包工具后，同一材质的粒子被切到相邻两页，合并时既要按页分开，又不能把同一材质的绘制顺序打乱；测试同学把粒子寿命调短之后，释放与发射在同一帧里反复交替，问题更容易复现。落点 gsb/batch48-go-game-b-21 的 particle_batch_compactor.go，类型 ParticleBatchCompactor，接口保持：NewParticleBatchCompactor(slotCapacity int) *ParticleBatchCompactor、SetBudget(b Budget) error、Budget() Budget、Emit(req EmitRequest) ([]int, error)、Compact() ([]Batch, error)、Release(ids []int) error、SlotCount() int、ActiveCount() int、QueueDepth() int、Resolve(id int) (SlotRef, bool)、SetBatchLimit(n int) error、BatchCount() int、DrainQueue(frame int) ([]int, error)、Stats() CompactorStats。BatchKey 由 Material、Blend、AtlasPage 组成，Blend 只接受 BlendAlpha、BlendAdd、BlendMultiply；EmitRequest 带 Key、Count、Frame、Priority、Lifetime；Batch 带 Key、Count、IDs；SlotRef 带 ID、Slot、Serial；Budget 带 MaxPerFrame、MaxQueued、MaxSlots；CompactorStats 带 Emitted、Queued、Dequeued、Rejected、Released、Reused、Batches。SlotCount 返回空闲实例槽数，Resolve 按 ID 给出槽位与序号，未知 ID 返回 false；SlotRef 的 Serial 从 1 起，每次槽位复用递增；构造时 MaxSlots、MaxPerFrame、MaxQueued 都等于 slotCapacity；合并结果按 AtlasPage、Material、Blend 升序稳定排列。

要修的行为：（1）Count 为 0 或负数照样被接收，调用方拿不到错误；（2）同材质、同混合模式、同图集页的两段发射合并后变成两个子批次，绘制调用翻倍；（3）合并结果里批次的先后顺序随内部遍历变化，相邻两帧的批次列表对不上，录像回放时特效层次乱跳；（4）同一键内粒子的 ID 不按升序排，渲染顺序跟着抖；（5）释放后槽位不回收，SlotCount 只减不增，合并结果也不随释放更新，长时间运行实例槽耗尽；（6）旧 ID 在槽位复用后仍能命中新粒子，释放老引用会把正在飞的粒子一起带走；（7）每帧发射没有硬顶，一次爆发请求吃光整帧槽位，负预算与把 MaxSlots 缩到活跃数以下也都不报错；（8）超预算的请求被直接丢弃而不是排队，下一帧再也看不到；（9）排队中的发射在后续帧没有按剩余预算继续吐出，QueueDepth 一直不减；（10）批次上限形同虚设，键一多就生成上百个子批次；（11）同一帧内释放后立刻复用槽位，合并结果里还留着已经失效的旧条目；（12）预算不足且队列已满时，请求既没有被拒绝也没有计数，Rejected 长期为零。

硬约束：并发发射时 ID 必须唯一且单调递增，统计计数不得丢失；槽位容量与批次数量都是硬顶，超限返回可识别错误而不是静默扩张；一帧内多次发射与多次排空之后，QueueDepth、SlotCount 与 Stats 必须彼此自洽。验收：go test ./... 15 条全过，不得修改测试、不得跳过、不得按固定输入写死；并发与跨帧排队自行验证；交付时附上合并前后的批次数对照与最坏帧的槽位占用说明；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-game-b-21`
- 包名：`task`
- 源码文件：`particle_batch_compactor.go`
- 测试目录：`tests/`
