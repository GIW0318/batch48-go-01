# batch48-go-game-b-23

## 题面

大世界切场景时显存忽然打满：贴图、顶点缓冲和渲染目标各自超了配额，界面上却显示还有余量；一张被钉住不释放的阴影图占着预算，驱逐接口返回成功但实际什么都没腾出来，下一帧纹理采样直接读到野指针，画面闪黑。传送门附近堆着三种类别的资源，缓存池本该把空闲的贴图额度临时让给顶点缓冲，实际却各自死守配额，一边爆满一边闲置；策划改了流式加载的优先级，最近用过的资源反而先被回收，玩家回头时整片地表重新上传。关掉垂直同步之后问题更频繁，开发机上是偶发，玩家机上是必现；内存分析工具显示贴图类别一直贴着配额上限，缓冲类别长期空着一半，两个类别加起来却超过总量。打包机上的老资源带着空的名字字段，请求进来就把已有句柄顶掉，之后谁也说不清哪个句柄还活着；回放系统每帧都读一次资源，老化时间戳被读操作刷新，真正该淘汰的常驻资源永远排不到前面。落点 gsb/batch48-go-game-b-23 的 gpu_memory_budget_arbiter.go，类型 GpuMemoryBudgetArbiter，接口保持：NewGpuMemoryBudgetArbiter(total Bytes) *GpuMemoryBudgetArbiter、SetTotal(b Bytes) error、SetQuota(c Category, b Bytes) error、Quota(c Category) Bytes、Request(r Resource) (Handle, error)、Release(h Handle) error、Pin(h Handle) error、Unpin(h Handle) error、Touch(h Handle) error、AdvanceFrame(frame int) error、Evict(need Bytes) ([]Handle, error)、Usage() Usage、HandleCount() int、Resolve(h Handle) (Resource, bool)。Resource 带 Name、Category、Size、Priority、AllocationID；Category 取 CatTexture、CatBuffer、CatTarget；Usage 带 Total、Used、Pinned、Handles、Borrowed、ByCategory；驱逐候选按最后使用帧升序、再按优先级升序、最后按句柄升序排列。

要修的行为：（1）Size 为 0 或负数照样占用句柄，未知类别被当成贴图处理；（2）同名资源二次申请不报错，旧句柄被静默覆盖；（3）类别用量已超配额时仍能继续申请，剩余预算没有跨类借用，Borrowed 永远为零；（4）总预算已经被占满时仍接受新申请；（5）释放句柄不回收预算，重复释放也不报错，Resolve 还能查到已释放的句柄；（6）同一句柄重复 Pin 不报错，未 Pin 的句柄 Unpin 也不报错；（7）驱逐时把钉住的资源一起收走，调用方下一帧拿不到资源；（8）候选全部被钉住时既不返回可识别错误，又已经删掉了一半资源；（9）驱逐顺序按句柄升序而不是最后使用帧，最近用过的资源反而先被回收，同帧同优先级之间也没有稳定次序，帧号回退时老化顺序直接错乱；（10）预算下调到当前用量以下之后，新申请仍然成功；（11）借出的预算在借出方重新申请时没有先收回，借用方还活着就被连带驱逐；（12）同一底层分配被两个句柄引用时重复计量，Usage 里的 Used 比实际大一倍。

硬约束：驱逐要么按需腾够空间要么整体不生效，不能留下半截状态；预算收缩先压容量再拒新请求，已有驻留不被顺手清掉；并发申请与驱逐时句柄与用量统计不得错乱。验收：go test ./... 15 条全过，不得修改测试、不得跳过、不得按固定输入写死；老化顺序与钉住保护自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-game-b-23`
- 包名：`task`
- 源码文件：`gpu_memory_budget_arbiter.go`
- 测试目录：`tests/`
