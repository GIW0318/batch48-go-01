# 任务包 batch48-go-game-a-28

## 模块

- module: gsb/batch48-go-game-a-28
- package: task
- file: behavior_tree_runtime.go

## 题面

怪物 AI 换成行为树之后，战斗节奏开始错乱。一组并行动作里有一个先失败，其余分支没有立刻停下，下一帧又接着跑，同一只怪同时做了两件互斥的事；节点预算调小之后，序列跑到一半就返回，下一次却把已经完成的子节点从头再跑一遍，掉落和技能各算了两次。黑板上写的数据也会串：子分支写进去的值，分支结束后还留在父层，下一轮把别的怪的仇恨值带了过来。回放里节点状态对不上，同一个 agent 两次跑出不同分支。落点 gsb/batch48-go-game-a-28 的 behavior_tree_runtime.go，类型 BehaviorTreeRunner，签名：NewBehaviorTreeRunner(Options) *BehaviorTreeRunner、Load(root Node) error、Loaded() bool、Tick(agent string, at int64) (Status, error)、Interrupt(agent, reason string) error、Blackboard(agent string) Blackboard、SetBlackboard(agent string, values map[string]int) error、SetBudget(agent string, nodes int) error、Trace(agent string) []Step、State(agent string) Status、Snapshot() ([]byte, error)、Restore([]byte) error。

要修：
一、Load 遇到空 ID、或条件节点比较符不在 eq、ne、gt、lt 之内，报 ErrInvalidNode；未知节点种类报 ErrUnknownKind；序列、选择、并行没有子节点报 ErrEmptyChildren。
二、未装载就 Tick 报 ErrNotLoaded；Loaded 在装载成功前为假。
三、condition 用黑板当前值与 Value 比较，缺键按 0 处理；set 写入 Key 并总是成功；两种叶子都要把自身 ID 与结果记入记录。尚未指定黑板的 agent 以 Options.Blackboard 为起始值。
四、sequence 顺序执行，遇失败立即失败且不再跑后面的子节点；selector 取第一个成功，全部失败才算失败；parallel 每帧推进全部子节点，任一失败则整体失败。
五、一次 Tick 最多展开 Options.Budget 个节点，可用 SetBudget 按 agent 覆盖；nodes 小于等于 0 报 ErrInvalidBudget，未登记 agent 报 ErrUnknownAgent。
六、预算在序列中间用尽时返回 StatusRunning，下一次 Tick 从断点继续，不能把已经完成的子节点重跑一遍。
七、并行节点有一个子节点失败时，其余子节点要按策略中断；被中断的分支状态不得在下一帧被当成仍在运行。
八、黑板的写入要落在确定作用域：子分支自己写下的值只在子分支内可见，分支结束即随子作用域销毁，父作用域的值不得被连带清掉。
九、Trace 每个被评估的节点记一条，按评估顺序追加，只保留 Options.TraceLimit 条最新的，时刻单调不减。
十、同一 agent 的 Tick 串行执行，不同 agent 互不阻塞；agent 之间黑板与预算完全隔离，读到的黑板是副本，改副本不影响内部状态；SetBlackboard 为 agent 建立起始黑板，Blackboard 对未登记 agent 返回空，不 panic。
十一、Interrupt 对未登记 agent 报 ErrUnknownAgent，对当前没有运行分支的 agent 报 ErrNotRunning。
十二、Snapshot 序列化树定义、各 agent 黑板、预算、游标与记录；Restore 遇坏数据报 ErrBadSnapshot 且原样保留，恢复后断点、状态与记录都能接着用。

硬约束（并发与原子性）：同一 agent 的 Tick 与 Interrupt 并发时，状态只落到一个确定结果，不得出现半完成的游标；不同 agent 之间不共享可变状态，一个 agent 卡住不得拖住另一个。

硬约束（快照与重启）：快照与 Tick 互斥，恢复失败不得改动任何 agent 的游标；重启后预算、游标与黑板自洽，已完成的子节点不会再跑一遍。

验收：go test ./... 15 条全过，不改测试、不跳过、不按固定输入写死；并发与重启自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```
