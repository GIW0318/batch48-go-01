# 任务包 batch48-go-game-a-29

## 模块

- module: gsb/batch48-go-game-a-29
- package: task
- file: spawn_point_guard.go

## 题面

刷怪系统的表现开始离谱。同一片营地五个刷怪点绑成一个刷新组，清掉其中一个点之后，其余点的计时各走各的，先清的先刷，后清的要等很久；玩家守在路口反复进出，怪一会儿扎堆一会儿空场。密度上限也有问题：区域里已经站满，客户端界面上仍显示可刷三只，点进去一只也不出，偶尔又一口气放出五只冲破上限。副本切相位之后，上一相位攒下的刷新在切回来时涌出来，把入口堵死。落点 gsb/batch48-go-game-a-29 的 spawn_point_guard.go，类型 SpawnPointGuard，签名：NewSpawnPointGuard(Options) *SpawnPointGuard、AddPoint(p Point) error、Kill(point, spawnID string, at int64) error、Ready(point string, at int64) (int, error)、Take(point string, n int, at int64) ([]Spawn, error)、SetPhase(phase string) error、Density(region string) int、Pending(point string) int、Phase() string、Snapshot() ([]byte, error)、Restore([]byte) error。

要修：
一、AddPoint 遇到空名、Interval 非正、Capacity 非正报 ErrInvalidPoint；同名重复登记报 ErrDuplicatePoint。
二、Kill 对未登记点报 ErrUnknownPoint，对不存在的刷新 ID 报 ErrUnknownSpawn；成功击杀要把该次死亡时刻记入计时基准。
三、就绪时刻是计时基准加 Interval；基准取该点最后一次 Take 与所在组最后一次死亡的较大值，未出过手的点以 Options.StartAt 为基准。Ready 只报告可刷数量，不占额度：未到就绪时刻为 0，到点后为 Capacity 减当前存活数；未登记点报 ErrUnknownPoint。
四、Take 才真正占额度：n 小于等于 0 报 ErrInvalidCount，未到就绪时刻报 ErrNotReady，返回数量同时受 Capacity 与区域密度上限约束，可能少于 n。
五、同组刷新点共享计时基准，整组从组内最后一个死亡时刻起算，不能各点各算。
六、密度到顶时 Ready 仍要返回可刷数量，Take 必须少给或不给；两个口径不一致会让客户端显示错。
七、区域内活体数量由 Density 统计，未登记区域返回 0；密度上限按 Options.RegionCap 的区域名匹配，未配置的区域不设上限。
八、返回的 Spawn 带唯一 ID、所属点、当前相位与发生时刻；同一时刻多次 Take 的 ID 不得重复。
九、SetPhase 遇到空相位报 ErrInvalidPhase；切换后新刷出的怪带新相位；旧相位排队中的刷新要作废，不能在切回时一次性放出。
十、被密度挡下的请求可以计入 Pending 排队，但不得占存活名额；排队数据不参与 Density。
十一、快照覆盖点位定义、存活 ID、计时基准、排队数与相位；Restore 遇坏数据报 ErrBadSnapshot 且原状态保留，恢复后计时与存活继续可用。
十二、同一名额不能给两次：并发调用 Ready 与 Take 时，Take 之间不得超发，存活数与密度统计要能对上。

硬约束（并发与原子性）：Take、Kill、SetPhase 共享同一份名额表，并发 Take 不得把同一个容量名额发给两个请求；Kill 释放名额与 Density 统计必须是原子的一步。

硬约束（快照与重启）：快照与写入互斥，恢复失败时原有计时与存活名单原样保留；重启后计时基准、存活 ID 与排队数自洽，已死亡的刷新不得复活。

验收：go test ./... 15 条全过，不改测试、不跳过、不按固定输入写死；并发与重启自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```
