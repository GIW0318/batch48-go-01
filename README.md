# 内存压力事件下的受害进程仲裁与回收额度分配

## 题面

节点内存告急时，回收脚本总要挑受害进程，但最近几次都先杀掉只有一份副本的前台服务，留下吃满内存的离线任务；事后既没有决策依据，也无法解释为什么某个进程被跳过。仲裁层因此拆成两步：先按压力事件出计划，再由上层确认提交，计划本身可以给人看、也可以回放。要修 gsb/batch48-go-safety-b-07 的 oom_arbiter.go，类型 OomArbiter，签名：NewOomArbiter(Options) *OomArbiter、Register(proc Proc) error、Pressure(ev PressureEvent) (Plan, error)、Commit(plan Plan) error、Protect(tenant string, pids []int) error、History(tenant string) []Decision、Snapshot() ([]byte, error)、Restore([]byte) error。

边界要求：
① 空租户、pid 非正、RSS 为零报 ErrBadProc；同 pid 重复注册报 ErrDuplicatePID，原记录不变，不能被后来的权重覆盖。
② 进程数超过 MaxProcs 报 ErrProcLimit，被拒进程不登记，已有进程的排序也不受影响。
③ 窗口非正或需求为零报 ErrBadEvent，事件非法时不得改动任何计划状态。
④ 候选按权重降序，同权重按 RSS 降序、pid 升序，排序必须稳定，同样输入每次得到同样顺序。
⑤ Pressure 只出计划不记账，Commit 之前 History 必须为空，重复调用 Pressure 不能累计流水。
⑥ Commit 才写流水并累计；计划窗口与最近事件不符报 ErrStalePlan，二次提交报 ErrPlanCommitted，两种情况都不写流水。
⑦ 回收按候选顺序填满需求，最后一个可部分回收且不标击杀；不够时如实报 Shortfall，不拿别的租户顶替。
⑧ 流水每租户按上限淘汰最旧，History 按租户隔离，一个租户的决策不出现在另一个租户里，顺序按提交先后。
⑨ 被 Protect 的进程权重再高也不进首选，保护过期后下一次事件恢复可选中，保护集合不能永久生效。
⑩ 同一窗口同一 pid 只计一次击杀，第二次只更新已释放字节；租户只剩唯一副本时计划标为不可杀并只报回收不足。
⑪ 计划的 Reclaimed 与 Shortfall 之和必须等于需求，Reclaimed 不超过受害者字节之和，Victim 的 Kill 与 Bytes 要能对上回收动作。
⑫ 快照保存进程表、保护与流水，恢复后 History 与 Proc 一致；坏快照报 ErrBadSnapshot 且不动现网。

常见错法：Pressure 里顺手把击杀写进流水；按注册顺序而不是权重排序；缺口补不满时跨租户借进程；Commit 只写流水不校验窗口，旧计划能把新事件的结果覆盖掉。

硬约束：并发上，计划与提交分离，同一窗口的 Pressure 与 Commit 必须串行，Commit 成功后流水与累计一次落定。容量上，进程表、每租户流水条数、单窗口回收上限都是硬顶。恢复上，重启后已提交的击杀不重复计数。

验收：go test ./... 15 条全过，不改测试、不跳过、不写死输入；除跑通用例，还要自己补并发与重启场景：同窗口并发 Pressure 与 Commit、提交后立即快照再恢复、保护过期后的下一轮计划；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-b-07`
- 实现文件：`oom_arbiter.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`OomArbiter`、`Options`、`Proc`、`PressureEvent`、`Victim`、`Plan`、`Decision`
