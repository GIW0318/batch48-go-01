# batch48-go-game-b-24

## 题面

开放世界边走边加载时，同一张贴图被十几个物件同时请求，结果发起了十几次解码，读盘队列堵满，远处地形迟迟不出现；后到的高优先级请求没有把在途加载提级，玩家冲到近处仍看到低清占位；最后一个请求方退出后加载还挂着，内存里堆了一排没人要的结果，下一次同键请求又从头解码一遍。武器皮肤与场景贴图共用同一个键空间，大小写和多余空格的写法各不相同，缓存命中率一直上不去；加载失败重试之后在途计数越叠越高，面板上的活动加载数永远降不到零。预加载阶段同一批键会被连续请求三四次，请求方拿到票据后各自持有，谁先退出就按自己理解的状态去处理；策划在关卡里放了大量重复引用的小物件，键空间很快撞到上限，新的加载请求被直接吞掉。监控面板上的合并计数与启动计数长期对不上，排查时无法判断到底省掉了几次解码。落点 gsb/batch48-go-game-b-24 的 resource_load_coalescer.go，类型 LoadCoalescer，接口保持：NewLoadCoalescer(maxKeys int) *LoadCoalescer、SetMaxKeys(n int) error、NormalizeKey(key string) string、Request(key string, prio int) (Ticket, error)、Complete(key string, res Payload, err error) error、Cancel(t Ticket) error、Status(key string) (State, error)、RefCount(key string) int、Priority(key string) int、InFlight() int、Resident() int、Result(key string) (Payload, bool, error)、Stats() CoalescerStats。Ticket 带 Key、Seq；Payload 带 Bytes、Format；State 取 StateIdle、StateLoading、StateReady、StateFailed；CoalescerStats 带 Requests、Started、Shared、Completed、Failed、Cancelled、Evicted、Resident、InFlight。优先级合法区间是 0 到 99；票据 Seq 从 1 起递增，失败重试视为一次全新加载，引用计数从 1 重新计。

要修的行为：（1）键没有做去空白与小写归一，大小写不同的同一资源被当成两条；（2）空键与只有空白的键照样建档，越界优先级也被接受；（3）同键第二个请求又发起一次实际加载，在途计数叠加，Started 涨到二；（4）后到的更高优先级没有提升整条加载的优先级，后到的更低优先级反而把已有优先级压低；（5）完成回调在非加载态也被接受，状态被改写；（6）加载结果没有按引用计数驻留，Complete 之后 Resident 仍为零，同键再来请求又发起一次加载；（7）加载失败后状态没有落到失败态，在途计数也不清；（8）失败后重试时在途计数继续叠加，Started 不增加；（9）最后一个请求方取消时加载没有整体取消，占位一直挂着；（10）仍有请求方时取消把整条加载也撤了；（11）未知票据取消不报错，键数量超过上限也不拒绝新键；（12）加载完成后取消一张已经无意义的票据，又去扣驻留引用甚至把结果淘汰，返回值也不稳定；同键不同优先级的请求先后取消时，优先级没有回退到剩余请求方的最大值。

硬约束：同键并发请求只能产生一次实际加载，在途与驻留计数在并发下不得丢失；键数量是硬顶，超限返回可识别错误；票据只能消费一次，取消与完成之间不得把同一条加载结算两遍。验收：go test ./... 15 条全过，不得修改测试、不得跳过、不得按固定输入写死；并发合并与取消路径自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-game-b-24`
- 包名：`task`
- 源码文件：`resource_load_coalescer.go`
- 测试目录：`tests/`
