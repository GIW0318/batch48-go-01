# 沙箱信号投递的域隔离与限速

## 题面

安全网关把信号投递收到用户态代理之后，同宿主不同业务域开始互相伤害：A 域的清理脚本一跑就把 B 域的运维进程打掉；另一次代理按 pid 缓存了两秒的放行结论，目标进程退出后 pid 被新进程复用，代理照着旧结论把终止信号投给了刚启动的数据库；监控同时报出信号风暴，单个投递方一秒内发出上千个终止信号，把目标进程的正常退出流程打乱。代理为了降低开销把判定结论留在内存里，注销与重新登记之间没有任何代际区分，注销路径只删了登记项却没有推进代际号，告警里能看到目标 pid 在毫秒级内换了一个进程名。登记表按 pid 建索引，查找结果不受登记顺序影响。落点 gsb/batch48-go-safety-b-14 的 signal_scope_guard.go，类型 SignalScopeGuard。接口保持：NewSignalScopeGuard(Options) *SignalScopeGuard、Register(proc Proc) error、Unregister(pid int) error、Allow(sig Signal, from, to int) (Verdict, error)、Deliver(sig Signal, from, to int) error、RateLimit(from int, now int64) (int, error)、Audit() []Record、Snapshot() ([]byte, error)、Restore([]byte) error、Lookup(pid int) (Proc, bool)、Registered() []Proc。

要修的行为如下。pid 非正或域名为空报 ErrBadProcess，空信号名报 ErrBadSignal；同 pid 重复登记报 ErrPidTaken，登记数到顶报 ErrRegistryFull；投递方或目标未登记报 ErrUnknownTarget。同域投递按放行处理，自己给自己发信号也放行；跨域投递默认拒绝，只有登记为看护方且信号在白名单里才放行。注销后的 pid 立即不可解析。每次投递无论允许还是拒绝都要留一条审计，包含信号、双方 pid 与结论码，结论码固定为 self_signal、same_domain、guardian_bridge、foreign_domain、rate_limited 五种。同一投递方按窗口内的条数限速，RateLimit 返回该投递方在当前窗口的剩余额度，预算用尽后再投返回 ErrSignalThrottled 且审计记限速，窗口滑过后预算恢复；限速按投递方分别记账，一个租户打满预算不影响其他租户。同域内 SIGKILL 与 SIGTERM 的限速口径不同，SIGKILL 不能被限速挡掉但要把计数记进审计；目标退出后 pid 被新进程复用，此前缓存的允许结论必须失效，投递要按进程启动时刻重新判定。快照要带上登记表、审计序号与各投递方已用预算。

硬约束：登记、注销、判定与投递会并发进入，同一 pid 的登记与注销只允许一个结果，信号投递与目标注销并发时只能有一个结果，不能审计记了投递成功而登记已删；审计序号严格递增不跳号；重启恢复后登记表、审计序号、限速窗口与各投递方已用预算要与中断前衔接，不能把已发出的信号重新算作可用预算；登记表规模与审计条数都有硬顶。

验收：go test ./... 的 15 条用例全部通过，测试文件不改、不跳过、不按固定输入写死；并发与重启恢复自行构造验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-b-14`
- 实现文件：`signal_scope_guard.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`SignalScopeGuard`、`Options`、`Proc`、`Signal`、`Verdict`、`Record`
