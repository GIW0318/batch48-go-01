# 租户进程创建的三重限额与有序终止

## 题面

某租户在节点上 fork 了上万个进程，把整台机器拖死。复盘发现扇出、树深、创建速率三道闸都没有真正生效，终止时又按登记顺序乱杀，把同租户的正常服务也带走了。这类事故的难点在于进程树一直在变：父进程先退出、子进程被过继、pid 被复用，登记表如果只记 pid 就会算错层级。要修 gsb/batch48-go-safety-b-05 的 fork_burst_guard.go，类型 ForkBurstGuard，接口为 NewForkBurstGuard(Options) *ForkBurstGuard、Admit(SpawnEvent) error、Attach(string, int, int) error、Reap(string) ([]int, error)、Snapshot() ([]byte, error)、Restore([]byte, func(int) bool) error。

边界要求：
① Admit 校验租户非空、pid 为正、ppid 非负，否则报 ErrBadTenant 或 ErrBadEvent，非法事件不得留下登记。
② 非根事件的父进程未登记报 ErrUnknownParent，登记数不变；同租户 pid 重复报 ErrDuplicatePID，原记录保持不变。
③ 子节点数达到 MaxFanout 再 fork 报 ErrFanoutExceeded；树深从 ppid 0 的根计 1，超过 MaxDepth 报 ErrDepthExceeded，越界进程不登记。
④ 令牌桶容量为 Burst，按 RatePerSecond 以毫秒粒度补充，取不到令牌报 ErrRateLimited，被拒请求不扣令牌也不登记。
⑤ 越界只封禁肇事租户，之后该租户的请求报 ErrTenantBlocked，其他租户的令牌与登记不受影响。
⑥ 违规流水按发生顺序记录租户、种类与 pid，种类区分 rate、fanout、depth，一次越界只记一条。
⑦ Attach 登记事后发现的进程，同一 pid/ppid 重复调用幂等，超过扇出或深度照常报错，不能因为叫 Attach 就绕过限额。
⑧ Reap 返回被终止子树的全部 pid，清空登记并解除封禁；没有违规时返回空且不动登记，不能顺手清表。
⑨ 终止超限子树先冻结再按叶子到根发信号，中途退出不算失败，也不重复计违规。
⑩ 父进程已退出但子进程仍登记时，深度沿存活祖先计算，死父不算第 0 层，否则整棵树会突然变浅。
⑪ Budget 报剩余令牌、容量、登记数与封禁状态；ProcessCount 与 Children 报登记视图，三者口径要一致。
⑫ 快照保存登记与令牌；恢复时按存活回调丢弃已消失节点并清理父子关系，回调为空报 ErrLivenessUnavailable，坏快照报 ErrBadSnapshot。

常见错法：把树深按当前登记层级算，父进程消失后深度突然变小；令牌桶按整秒补充，跨秒边界能白拿一整秒额度；Reap 按登记顺序杀，父进程先死导致子进程变成孤儿。

硬约束：并发上，登记表用 sync.RWMutex，Admit 与 Attach 的判定加登记必须串行，Reap 冻结子树期间不得把新进程挂进被冻结的子树。容量上，扇出、树深、令牌桶容量都是硬顶。恢复上，恢复后的树与令牌和快照一致，已消失节点不残留。

验收：go test ./... 15 条全过，不得改测试或写死输入；除跑通用例，还要自己补并发与重启场景：同租户并发 fork 到扇出上限、Reap 与 Admit 交叉、快照后在新 guard 上继续登记；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-b-05`
- 实现文件：`fork_burst_guard.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`ForkBurstGuard`
