# 降权阶梯的分阶段推进与凭据回读

## 题面

采集服务以 root 启动，启动脚本按一份阶段表把进程逐级降到业务账号，顺序是先补附加组、再降 gid、再降 uid、最后置 NoNewPrivs。值班回放时发现两个问题：降 uid 之后能力掩码还留着，进程仍能做提权动作；补组失败时逻辑直接退回 root 重新开始，重启以后既说不清走到了哪一级，也拿不出每一步回读到的真实凭据。要修 gsb/batch48-go-safety-b-01 的 privilege_stage_guard.go，类型 PrivilegeStageGuard，签名保持：NewPrivilegeStageGuard(Plan) (*PrivilegeStageGuard, error)、Run(stage string) (StageResult, error)、Current() Credentials、Verify(stage string) error、Snapshot() ([]byte, error)、Restore([]byte) error。

边界要求：
① 空阶段表、阶段重名、依赖指向不存在的阶段、依赖指向更晚的阶段、种类顺序被调换、可逆阶段被标成不可逆，构造时一律报 ErrPlanInvalid，不返回可用对象。
② 阶段表必须恰好包含补组、降 gid、降 uid、置 NoNewPrivs 各一次，顺序固定；依赖只允许指向更早的阶段。
③ 跳过前置 Run 直接推进后续阶段报 ErrOutOfOrder，当前凭据一个字段都不许动；阶段名不在表内报 ErrUnknownStage。
④ 补组阶段把组列表升序去重后写入，euid 保持 0；同一阶段重复 Run 只生效一次，流水不重复记。
⑤ 降 gid 阶段只改 egid，附加组原样保留；降 uid 阶段把 euid 改成目标值并把能力掩码清零，已保存的 suid 不能把能力带回来。
⑥ 置 NoNewPrivs 之后 Current 必须能看到该标记；Journal 按完成顺序记录，序号严格递增，每条都要带当时的凭据与回读值。
⑦ 回读只比较本阶段应当改变的字段，Probe 返回值与预期不符报 ErrReadbackMismatch，该阶段不得记为完成，后续阶段也不能越过它。
⑧ Verify 对已完成阶段返回 nil，对未完成阶段报 ErrStagePending，对未知阶段报 ErrUnknownStage；三种情况不能混淆。
⑨ 已降 gid 之后再补组必须报错并保持现状；降权后回退到更早阶段，即使外部恢复了保存的 suid，也必须拒绝，且不得把能力掩码写回去。
⑩ 中途失败停在当时级别，已完成位保持，Verify 给出逐项差异，不允许整体回滚成 root。
⑪ 快照承载完成位与每一步的回读值；恢复后不重跑即可 Verify 通过，Current 与 Pending 和快照时一致，Completed 的顺序也不能变。
⑫ Completed、Pending 都按计划顺序返回，恢复后与快照时相同；坏快照报 ErrBadSnapshot，且不改动现状。

容易写错的地方有三处：把 Current 当成阶段表里的计划值直接返回，而不是回读到的真实凭据；只在最后统一比较一次，导致中间阶段的偏差被后一步覆盖；快照只存完成位、不存回读值，恢复以后 Verify 无从比对。

硬约束：并发上，Run 与 Verify 同时打到同一阶段时只能生效一次，完成位与流水的写入必须串行，Current 不得看到半个阶段的中间态；快照与判定互斥，读取方只能看到全量旧态或全量新态。恢复上，Restore 必须一次性替换，失败不留半份状态，重复恢复同一份快照结果一致，重启后不回退已完成的降权。

验收：go test ./... 15 条全过，不得改测试、跳过或写死输入；除跑通用例，还要自己补并发与重启场景：多个 goroutine 同时 Run 同一阶段、Verify 与 Run 交叉、快照后在新 guard 上继续推进；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-b-01`
- 实现文件：`privilege_stage_guard.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`PrivilegeStageGuard`
