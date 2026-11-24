# 任务包 batch48-go-game-a-26

## 模块

- module: gsb/batch48-go-game-a-26
- package: task
- file: navmesh_path_guard.go

## 题面

副本里的怪物寻路上线后，追击表现时好时坏。走过泥地和碎石的路径，界面预估与战斗结算对不上；贴墙斜切过去的路线看起来更短，扣掉的行军时间却是另一套数。峡谷图临时封锁一段通道后，已经算好的路径照样在用，怪物一头撞进封锁格原地打转，封锁解除很久缓存也不更新。目标站进水里或封锁格时，整次追击直接中断，怪物呆立不动，而策划要的是先跑到最近的一点再停。开图时划走一片区域，受影响的缓存路径同样要立刻作废。落点 gsb/batch48-go-game-a-26 的 navmesh_path_guard.go，类型 NavmeshPathGuard，签名：NewNavmeshPathGuard(Grid, Options) (*NavmeshPathGuard, error)、Find(from, to Point, budget int) (Path, error)、Smooth(path Path) Path、Block(cells []Cell, until int64) error、Blocked(p Point, at int64) bool、ClearExpired(at int64) int、Invalidate(region Region) error、PathCost(path Path) int、Stats() Stats、Clock() int64、Snapshot() ([]byte, error)、Restore([]byte) error。

要修：
一、Grid 行列非正、Tier 长度不等于 Width 乘 Height、档位超出 0 到 3，报 ErrInvalidGrid；BaseCost、DiagonalNum、DiagonalDen、MaxExpanded 非正，报 ErrInvalidOptions。
二、坐标越界或为负报 ErrOutOfRange，budget 小于等于 0 报 ErrInvalidBudget，起点落在档位 0 的实心格报 ErrWallStart。
三、正交进入一格的代价是 BaseCost 乘目标格档位；对角一步是档位乘 BaseCost 再乘 DiagonalNum 除以 DiagonalDen，整数截断；对角移动要求两侧正交格都能通行，禁止切角。
四、结果的 Points 从起点排到终点，Cost 是整条路径的代价和，Start 回填本次实际起点，可直达时 Partial 为假。
五、目标被实心格或障碍围死时返回部分路径：Partial 为真，终点是离目标最近的可达格，距离相同取 Y 小的、再取 X 小的，不报错。
六、budget 计本次展开的格数，用尽仍未到达报 ErrBudgetExhausted；传入预算超过 MaxExpanded 时按 MaxExpanded 截断。
七、起点被临时障碍占住时不得直接判不可达，要改从最近可用格出发，并把修正后的起点写进结果。
八、Block 先整体校验再落盘，任一格越界报 ErrOutOfRange 且一格都不生效；until 不晚于当前时钟报 ErrInvalidBlock；同一格重复 Block 按最新到期时刻覆盖；Blocked 在 at 小于 until 时为真，到达 until 即为假；ClearExpired 把时钟推进到 at，删掉到期障碍并返回条数。
九、Cost 与 LowerBound 分别回填，LowerBound 是任意可行路径的下界；启发式与地形档位不一致导致次优路径时，Cost 必须如实反映，不得拿下界冒充实际代价或用 Partial 掩盖。
十、Smooth 只删共线中间点，合并后用 PathCost 重算代价，输入路径不得被改动；PathCost 遇到非相邻跳步返回 -1。
十一、同一 from、to、budget 在障碍未变时复用缓存，Queries 每次加一、CacheHits 命中加一；Invalidate 删掉与区域相交的缓存，区域非法报 ErrOutOfRange；ClearExpired 删掉障碍后相关缓存必须失效。
十二、Snapshot 写入地形、时钟、存活障碍与缓存；Restore 遇坏数据报 ErrBadSnapshot 且状态原样保留，恢复后时钟、障碍与缓存自洽。

硬约束（并发与原子性）：Find 只读一份一致的障碍快照，Block、ClearExpired、Invalidate 与 Find 并发时单次查询不能看到一半新一半旧的障碍；Block 的多格写入要么全成要么全不成。

硬约束（快照与重启）：快照与写入互斥，重启恢复后过期障碍不得重新挡住路径，缓存不得引用已经不存在的障碍。

验收：go test ./... 15 条全过，不改测试、不跳过、不按固定输入写死；并发与重启自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```
