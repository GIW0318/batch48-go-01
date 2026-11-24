# UEFI 变量更新的签名与回滚保护

## 故障描述

安全运营在例行核对时发现：一批 db 条目在重启之后全部消失，同一批次里被撤销的哈希第二天又回到了允许清单；另一次更新把单调回滚计数改回上个月的数值，平台侧的回滚保护形同虚设。落点 gsb/batch48-go-safety-d-12 的 uefi_var_update_guard.go，类型 UefiVarGuard，签名保持：NewUefiVarGuard(Limits) *UefiVarGuard、SetSecureBoot(bool) error、SecureBoot() bool、StageUpdate(string, []Op, Chain, uint64) (Batch, error)、Validate(Batch) (Report, error)、Commit(Batch) error、Revoke(string) error、SetTrustAnchor(Entry, Auth) error、Entries(string) []Entry、RollbackCounter() uint64、Snapshot() ([]byte, error)、Restore([]byte) error、Stats() Stats。

要修：变量只认 db、dbx、KEK，其它名字一律拒绝，操作列表为空或模式不认识也要报错；更新载荷的签名链必须带签名者，签名以 sig: 开头，否则整批拒绝；时间戳必须严格大于上一次成功提交的时间戳，相等或倒退都算过期；追加操作逐条比对撤销集合，命中任一哈希整批拒绝，不得只跳过那一条；追加把条目并入变量尾部，删除按哈希精确摘除，删除不存在的条目报错，两种模式语义不可混用，哈希为空的条目直接拒绝，重复追加同一条目同样拒绝，同一批次内多个操作按给出的先后顺序结算，后面的操作能看见前面操作的结果；信任锚变更必须携带授权标记，空标记或前缀不是 trust: 的一律拒绝；每个变量有独立配额，结算后超出配额整批拒绝，不得截断已有条目；回滚计数只能递增，提交小于或等于当前计数的批次必须拒绝；快照保存变量内容、撤销集合、时间戳水位与回滚计数，恢复后计数与水位不得回退，损坏的快照拒绝恢复且现网数据不变；删除 db 中最后一条目会让安全启动失效，必须先解除安全启动状态才允许执行该删除；黑名单条目只增不减，配额耗尽时拒绝新增并保留既有条目，不得淘汰最旧条目腾位；统计分别记录已暂存、已提交、被拒绝与撤销操作的次数。

硬约束：批次从暂存到提交之间必须是原子的，校验失败或提交中断都不得留下半批数据；同一变量同时只允许一个批次处于暂存状态，后来的暂存覆盖前一个，提交成功后暂存位清空，重复提交同一批次必须报错；快照必须在提交边界上产生，恢复过程中不接受新批次；重启后撤销集合、时间戳水位、回滚计数与变量内容必须与快照一致。

验收：go test ./... 的 15 条用例全过，不得修改测试、跳过用例或按固定输入写死结果；事务与重启恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

模块路径 `gsb/batch48-go-safety-d-12`，包名 `task`，实现文件 `uefi_var_update_guard.go`，用例位于 `tests/`。
