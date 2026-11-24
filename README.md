# 可疑进程隔离、取证与分级终止

## 题面

值班同事把一个可疑挖矿进程拉进隔离，第二天复盘发现隔离期间它还在写盘，取证快照里只有半份打开文件清单；另一次误隔离了构建机的编译进程，人工放行后编译任务仍然起不来，原因是原本停住的进程已经被系统回收，放行作用到了复用同一个 pid 的新进程上；还有同一台机器短时间内对同一进程重复报案，最后生成了好几条互相冲突的案件。隔离区入口没有统一编号，巡检与人工操作各记一套，复盘时对不上哪次放行对应哪次采集；取证包还被下游系统退回，理由是清单不完整、无法证明停住的就是原进程。落点 gsb/batch48-go-safety-b-12 的 quarantine_guard.go，类型 QuarantineGuard。接口保持：NewQuarantineGuard(Options) *QuarantineGuard、Quarantine(pid int, reason, by string) (Case, error)、Collect(caseID string) (Evidence, error)、Release(caseID, by string) error、Terminate(caseID, by string) error、Expire(now int64) ([]Case, error)、Cases(state string) []Case、Snapshot() ([]byte, error)、Restore([]byte) error、Lookup(caseID string) (Case, bool)、Active(pid int) (Case, bool)。

要修的行为如下。pid 非正、原因为空、操作者为空或进程身份解析不出来，分别报 ErrBadCase 与 ErrProcUnavailable。新案件进入待确认状态并记下来源，案件数达到上限后拒收新案件。采集动作只允许在待确认状态执行，成功后案件转入已停住并带上采集时刻；文件清单要去重、按字典序排列，超过上限按序截断；重复采集同一案件报 ErrStateConflict。放行只允许作用在已停住案件上并记下操作者，终止同样只允许从已停住发起；到期先升一级并发终止请求，宽限期过后升二级并标记已终止，同一时刻重复调用不得重复升级。终止时进程若已被外部杀掉，要按已完成收敛且不报错；同一 pid 短时间内重复报案只能有一个活动案件，第二次挂到已有案件上并记来源。案件编号由守卫单调生成，同一次请求重复提交不得生成两个编号；放行与终止后案件都不得再次进入活动列表。案件列表按状态过滤，传空状态返回全部；快照要带上每个案件的状态、证据摘要与计时信息。

硬约束：隔离与到期扫描会被多个巡检协程并发调用，同一案件的状态只能推进一次，不能出现一边释放一边终止；快照跨进程重启恢复后，待确认与已停住案件必须能继续按原截止时刻计时，已放行和已终止的案件不得回到活动态；证据文件条数与在办案件数都有硬顶，不能随巡检轮次增长。

验收：go test ./... 的 15 条用例全部通过，测试文件不改、不跳过、不按固定输入写死；并发与重启场景自行构造验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-b-12`
- 实现文件：`quarantine_guard.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`QuarantineGuard`、`Options`、`ProcRef`、`Case`、`Evidence`
