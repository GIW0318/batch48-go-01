# Go 实现题

## 题面

投诉：一条委托的上游被撤销后下游仍在放行；把权限名当前缀做包含判断，只读委托拿到只读加写；同一权限由两条链授予，撤销一条后另一条也失效。落点 gsb/batch48-go-safety-a-10 的 delegated_grant_ledger.go，类型 DelegatedGrantLedger，包名 task。

签名保持：NewDelegatedGrantLedger(opts LedgerOptions) *DelegatedGrantLedger、Seed(subjectID string, perms PermissionSet) error、Delegate(fromID, toID string, perms PermissionSet, expiresAt time.Time) (Grant, error)、Accept(grantID string) error、Effective(subjectID string, at time.Time) (PermissionSet, error)、Revoke(grantID string) (int, error)、Cascade(subjectID string) (int, error)、Snapshot() ([]byte, error)、Restore(data []byte) error。

要修：求值结果按资源与动作排序去重，未接受的委托不计入；每一跳必须是委托人当前权限的子集，按资源与动作精确比较而非名字前缀；委托人自身权限在存续期内被回收时下游委托下次求值立即失效，不等自然过期；到期按传入时间点比较，到期那一刻起不生效；再委托记录深度与来源，超上限拒绝；单主体持有委托数设上限；求值要给出每条权限的来源链，从根委托到最末一跳；撤销上游级联失效全部下游并返回含自身的条数；同一权限被多条链授予时撤销其中一条不得影响其余来源；按主体级联撤销只清它签发的委托，它收到的委托不受影响；未知委托重复撤销报错；快照保留种子、委托、接受与撤销状态。

硬约束：并发再委托与撤销不得留下仍生效的孤儿授权，同一委托并发撤销只能成功一次且只计一次；重启后按快照求值与撤销前逐条一致。验收：go test ./... 15 条全过，测试不得修改、跳过、写死；并发与重启自行验证；只做本地实现。

## 运行方式

```bash
cd batch48-go-safety-a-10
go test ./...
```

## 模块信息

- module: gsb/batch48-go-safety-a-10
- 源文件: delegated_grant_ledger.go
- 包名: task
- 测试目录: tests/
