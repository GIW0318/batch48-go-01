# batch48-go-safety-c-03

模块路径 `gsb/batch48-go-safety-c-03`，包名 `task`，实现文件 `taint_flow_tracker.go`，主类型 `TaintTracker`。

## 题目

代码审计流水线在过程间污点分析这一步频繁误报和漏报：同一个变量先经过过滤函数又被重新赋值成新输入，工具仍把它当已净化，漏洞直接漏掉；递归函数的摘要跑十几分钟不收敛，最后干脆不输出任何结论，汇点全被丢掉；别名赋值只单向传播，a 赋给 b 之后从 b 来的污染到不了 a；净化点只擦掉一个标签却把同一变量的其他标签一起清空；同一张图跑两次，汇点列表顺序都不一样，报告没法比对。预算参数形同虚设，路径爆炸时进程内存被打满；现场保留的图动辄几十万条边，还要支持边增量加载，重启后同一张图的汇点集合又和上次对不上。

请修 `gsb/batch48-go-safety-c-03` 模块里的 `taint_flow_tracker.go`，类型名 `TaintTracker`，包名 `task`，只用标准库。

接口保持：`NewTaintTracker(Options) *TaintTracker`、`MarkSource(node string, labels []string) error`、`AddSink(node, kind string) error`、`Propagate(from, to string, kind EdgeKind) error`、`ApplySanitizer(node string, labels []string) error`、`SetSummary(fn string, s Summary) error`、`Solve() (Solution, error)`、`ReachableSinks() ([]SinkHit, error)`、`Labels(node string) ([]string, error)`、`SetBudget(maxPaths, maxIterations int) error`、`Reset() error`、`Stats() SolveStats`、`Nodes() []string`。

要落到行为上的十一条：

一、`MarkSource` 节点为空或标签为空返回 `ErrBadNode` 或 `ErrEmptyLabels`；同一个节点多次标源按标签并集合并。

二、`AddSink` 同一节点同一类型重复登记返回 `ErrDuplicateSink`；节点为空返回 `ErrBadNode`。

三、`Propagate` 的边类型只认赋值、别名、调用三种，其他值返回 `ErrUnknownEdge`。

四、别名边双向传播，赋值边单向；标签集合始终去重并升序。

五、`ApplySanitizer` 只移除点名的标签，未点名的标签继续向下游流动；对未登记节点调用返回 `ErrUnknownNode`。

六、净化点之后的路径不得再命中汇点：被净化标签在该节点出边方向被剪掉，不能靠绕路恢复；变量先净化、随后又被重新赋值成新污染时，新标签必须能重新流向下游。

七、调用边只认已经登记且完整的函数摘要，摘要里的返回标签在求解时传播到调用点；找不到摘要返回 `ErrNoSummary` 并带函数名。

八、求解前调用 `ReachableSinks` 或 `Labels` 返回 `ErrNotSolved`，中间态一律不对外可见。

九、`ReachableSinks` 按节点、类型升序，标签升序去重；`Solve` 返回的 `Solution.Sinks` 必须与之一致。

十、同一张图重复 `Solve` 得到逐条一致的结果与统计，不依赖 map 遍历。

十一、`SetBudget` 只接受正数，否则返回 `ErrBadBudget`；求解超出迭代上限时标记 `Solution.Complete` 为 false、`Stats.Truncated` 为 true，迭代次数停在上限。递归摘要未收敛就被截断时，汇点可达性按保守方向给出，不得凭空丢弃。

硬约束：求解期间不得暴露半成品标签；`Reset` 之后节点、边、汇点、统计全部清零，再次调用 `ReachableSinks` 返回 `ErrNotSolved`；同一份图换一种加载顺序再求解，汇点、标签与迭代次数必须完全一致。验收：`go test ./...` 十五条必须全过，不改测试、不跳过、不按固定输入写死；并发与重复求解自行验证；只做本地实现。

## 运行

```
go test ./...
```
