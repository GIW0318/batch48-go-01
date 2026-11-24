# 任务包 batch48-go-game-a-20

## 模块

- module: gsb/batch48-go-game-a-20
- package: task
- file: tick_budget_governor.go

## 题面

固定步长的对战服在大厅满载时出现规律性卡顿：物理与网络两个子系统每几秒一起超支，下一个 tick 里被跳过的 AI 又一次性把积压工作补完，卡顿翻倍；运维把降级等级手动调到中挡之后，只要关键系统还在超支，等级就再也不动了；更奇怪的是滚动重启后，原本已经降级的分片立刻回到未降级状态，重启后的第一个 tick 就把预算打满。监控面板上还能看到某些系统整 tick 一行支出都没有，下一 tick 却冒出双倍数字，账实对不上。落点 gsb/batch48-go-game-a-20 的 tick_budget_governor.go，类型 TickBudgetGovernor，签名保持：NewTickBudgetGovernor(totalMs int) *TickBudgetGovernor、Register(system string, weight float64, minMs int) error、BeginTick(now time.Time) error、Spend(system string, ms int) error、EndTick() (TickPlan, error)、SetDegradeLevel(n int) error、DegradeLevel() int、Remaining(system string) int、Stats(system string) (SystemStats, error)、Snapshot() ([]byte, error)、Restore(payload []byte) error。

要修：注册系统时记录权重与保底，重复注册返回 ErrSystemExists，权重不大于 0 返回 ErrInvalidWeight，保底为负返回 ErrInvalidMin，首个 tick 开始后注册一律返回 ErrRegistrationClosed；每 tick 先为各系统保留保底，剩余预算按权重向下取整分配，总配额不得超过总预算；BeginTick 打开新 tick，重复打开返回 ErrTickInProgress，EndTick 在未打开时返回 ErrTickNotStarted，EndTick 返回的 TickPlan 要带本 tick 序号、总预算、各系统配额表，以及超支、降级、跳过清单；Spend 只接受正数金额，0 或负数返回 ErrInvalidSpend，未知系统返回 ErrSystemUnknown；系统在自身配额内支出直接通过，Remaining 随之下降，超出配额只能借用其他系统本 tick 未用的配额，全 tick 总支出不得超过总预算，否则返回 ErrBudgetExceeded 且状态不变；借用额度与超支次数计入 Stats，超出配额的部分结转到该系统的超支累计，作为阶梯判定依据，超支累计跨 tick 保留，不会因为一个空闲 tick 清零；连续两个 tick 超支让降级等级加一，上限 3，全部系统连续三个 tick 不超支才降一级，下限 0；降级等级下非关键系统按等级降低执行频率，保底大于 0 的关键系统永不跳过，被跳过系统本 tick 的 Spend 返回 ErrSystemSkipped，跳过按系统分别记录，便于事后对账；被降级跳过的系统本 tick 的积压工作不得在下个 tick 一次性补做，避免二次超支；降级期间关键系统仍持续超支时要继续升级等级，不能停在当前级；快照恢复后降级等级与连续达标计数要一起恢复，否则重启会立刻取消降级；预算统计用单调时钟，BeginTick 的时间早于上一 tick 返回 ErrClockBackwards。

硬约束：Spend 与 BeginTick、EndTick 由互斥锁加原子计数保护，任意并发下全 tick 总支出都不得超过总预算，同一系统不会因为并发借用而透支；配额、缓冲与降级等级都有硬上限；快照必须自包含，恢复后不得出现半生效的 tick 状态。

验收：go test ./... 15 条全过，不得改测试、跳过或按固定输入写死；并发与重启恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```
