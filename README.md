# mip 流送调度：父级依赖、屏幕占比优先级与带宽硬预算

## 题面

大世界开镜后远处地形先糊成一块，随后又突然变清，帧时间每隔几帧就冒一个尖刺；同一个纹理的 2 级 mip 比 0 级先上传，采样时读到还没驻留的层级，屏幕边缘闪出灰块；低优先级的地表纹理在队列里等了一百多帧始终排不上，取消过的请求下一帧又出现在上传列表里。落点 gsb/batch48-go-game-b-17 的 mip_stream_scheduler.go，类型 MipStreamScheduler，接口保持：NewMipStreamScheduler(maxLevels int) *MipStreamScheduler、SetMaxLevels(n int) error、RegisterTexture(texID, width, height int) error、TextureLevels(texID int) (int, error)、MipBytes(texID, mip int) (int, error)、Request(texID, mip int, priority float64) error、Drain(budget int) ([]Upload, error)、Cancel(texID, mip int) error、Resident(texID int) (int, error)、PendingLevels(texID int) ([]int, error)、PendingCount() int、Frame() uint64、SetMaxPending(n int) error、Stats() SchedulerStats。Upload 带 TexID、Mip、Bytes、Priority、WaitFrames；SchedulerStats 带 Textures、Pending、Uploaded、Canceled；等待一帧的优先级增量记作 AgingStep，取 0.5。

要修的行为：纹理注册接受 0 或负的宽高，重复注册同一 texID 时直接覆盖成新尺寸，级数也不按上限封顶；mip 字节数算成整张纹理大小，没有按每级宽高各取至少 1 后相乘再乘 4；Request 收到未知纹理、越界 mip、NaN 或负优先级全部照收；子 mip 在父 mip 尚未驻留时就被排进上传列表；缺父级时 Request 直接报错，而不是把请求挂起等父级；同一纹理同一 mip 重复请求产生多条上传项，优先级取后到的较低值；Drain 按估算字节而非硬预算挑选，单帧总字节超过传入预算；被跳过的请求不累计等待帧，低优先级请求一直饿死；Cancel 之后请求仍出现在上传列表；驻留等级会被后来的低 mip 请求改小；队列没有上限，任何请求都能无限堆积。

还有两条交错边界没有明确判据：父 mip 驻留与子 mip 取消落在同一帧时，子级挂着的依赖要不要当场释放；同一纹理先请求细 mip、随后改请求更粗 mip 时，队列里更细的那条排队项是保留还是清掉。

硬约束：Request、Drain、Cancel、Resident、统计在同一把锁下串行，同一 mip 在一次 Drain 里最多上传一次，并发下驻留等级单调不降；每帧字节预算是硬顶，放不下的请求留到下一帧而不是截断或超发；依赖在 Drain 开始时判定，本帧刚上传的父级要到下一次 Drain 才解锁子级，同一请求重复提交合并为一条并取最高优先级。Drain 的挑选顺序是有效优先级降序，有效优先级等于请求优先级加等待帧数乘 AgingStep，同值时 texID 升序、mip 升序；未被选中的请求等待帧数加一。注册时级数为 1 加 max(width,height) 的二进制对数再与 maxLevels 取小，每级字节为 max(1,width>>mip) 乘 max(1,height>>mip) 再乘 4；同尺寸重复注册幂等，换尺寸报 ErrTextureExists；Resident 在没有驻留时返回 -1；Cancel 已驻留层级报 ErrAlreadyResident，不在队列报 ErrNotPending；Drain 预算非正报 ErrBadBudget，SetMaxLevels 与 SetMaxPending 非正报 ErrBadLimit；Stats 的 Textures 为已注册纹理数、Pending 为当前排队数、Uploaded 为累计上传条数、Canceled 为累计取消条数。

验收：go test ./... 的 15 条用例全部通过，不得改测试、跳过或按固定输入写死；并发请求与并发 Drain 自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-game-b-17`
- 实现文件：`mip_stream_scheduler.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`MipStreamScheduler`、`Upload`、`SchedulerStats`
