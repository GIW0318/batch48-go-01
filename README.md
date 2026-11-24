# 容器退出码分类与崩溃循环熔断

## 题面

凌晨两点容器平台大面积不可用：一个依赖配置的边车因为配置写错反复退出，重启调度把它当成偶发崩溃，退避窗口一轮比一轮长，最终整组副本被熔断，值班上去手动拉起也没能把计数清掉；同一时刻另一批被外部杀掉的容器却被立刻反复拉起，把节点 CPU 打满。值班照着手册逐个重启副本，发现有的容器退避已经停在最大值，有的容器却连一条重启记录都没有，退出原因与人工操作日志对不上账。落点 gsb/batch48-go-safety-b-11 的 container_restart_guard.go，类型 RestartPolicyGuard，负责把一次退出上报翻译成重启决策。接口保持：NewRestartPolicyGuard(Options) *RestartPolicyGuard、Report(container string, status ExitStatus, at int64) (Decision, error)、Due(container string, now int64) (int64, bool)、CircuitOpen(container string) bool、Reset(container string, by string) error、History(container string) []Attempt、Snapshot() ([]byte, error)、Restore([]byte) error、Backoff(attempts int) int64、Crashes(container string) int。

要修的行为如下。退出码为 0 判正常退出，不安排重启；退出码命中配置错误集合时判配置错误，既不退避也不累计崩溃；内存终止造成的非零退出判可重试失败，按指数退避安排下一次启动；非内存原因的信号击杀判外力杀死，只记录不重启。正常退出与收到正常终止信号都算正常停止，只有非零退出和内存终止才进退避，两类不能共用同一个崩溃计数。退避时长按尝试次数逐次翻倍、到上限截断，并在其上叠加确定性抖动，幅度不超过配置的百分比；退避结果里同时给出毫秒延迟与绝对时刻，两边口径必须一致；Due 返回下次重启的绝对时刻，并说明当前 now 是否已经到点；正常停止要取消尚未执行的重启安排。连续崩溃达到上限后进入熔断，熔断期间再次上报返回 ErrBreakerOpen 且不再安排重启；熔断期间编排侧反复上报同一容器，历史只能新增一条，不能每次上报都记一次。Reset 由人工携带操作者身份执行，清空计数与熔断并允许下一次重启；人工拉起后容器稳定运行超过配置窗口，崩溃计数要清零，不能带着旧计数继续累计到熔断。

硬约束：Report 与 Due 会被多个调度协程并发调用，同一容器的状态推进只允许发生一次，不得出现两个协程各算一次退避；History 按时间单调，内存与上报记录受条数上限硬顶，不能随运行时长无限增长；快照要能跨进程重启恢复每个容器的计数、熔断状态和下次重启时刻，恢复后行为与中断前一致。

验收：go test ./... 的 15 条用例全部通过，测试文件不改、不跳过、不按固定输入写死；并发与重启恢复自行构造验证；只做本地实现。
以上现象均在真实生产环境复现。
## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-b-11`
- 实现文件：`container_restart_guard.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`RestartPolicyGuard`、`Options`、`ExitStatus`、`Decision`、`Attempt`
