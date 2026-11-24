# 加密卷密钥槽的解锁节流与 rekey

## 故障描述

运维接到一起加密盘事故：某个密钥槽被连续试错锁定后，值班人员用恢复密钥也解不开，事后核对发现失败计数被清零、节流策略失效；同一晚一次 rekey 中途断电，活动槽指向新槽，可新槽从未验证可解，卷直接打不开。落点 gsb/batch48-go-safety-d-14 的 luks_keyslot_guard.go，类型 KeyslotGuard，签名保持：NewKeyslotGuard(Limits) *KeyslotGuard、AddSlot(Kind, string, string) (int, error)、Unlock(int, Credential) error、Rekey(int, Credential) (RekeyResult, error)、RemoveSlot(int) error、SetActive(int) error、Active() int、Slots() []SlotInfo、Locked(int) bool、CredentialPath(Kind) (string, error)、WriteActive([]byte) (int, error)、Snapshot() ([]byte, error)、Restore([]byte) error、Stats() Stats。

要修：密钥槽编号按配额单调分配，删除之后编号不复用，配额用尽报错；口令、密钥文件、恢复密钥三类凭据的类型必须与槽位类型一致，不一致直接拒绝，三类凭据的读取路径必须彼此分离；口令为空或类型不认识时拒绝建槽；错误口令按槽位独立累计失败次数，达到阈值即锁定该槽，锁定之后正常口令也一律拒绝，某个槽位锁定不影响其它槽位继续尝试，失败原因要区分槽位不存在、类型不符、已锁定与口令错误；槽位标签允许重复，但同一槽位编号只会出现一次，切换活动槽只能切到存在且未锁定的槽位，切换失败保持原活动槽；槽位锁定期间用恢复密钥解锁必须成功且不清零该槽位的失败计数，计数只在正常口令解锁成功后清零；rekey 先分配新槽、验证新凭据可解，再原子切换活动槽，任何一步失败都要保持原活动槽与原槽位集合不变；rekey 期间卷处于只读状态，此时的写入请求要排队或被拒，不能落在正在迁移的槽上；活动槽不允许直接删除，删除前必须先切换；损坏快照拒绝恢复且不破坏现网槽位；快照只保留槽位元数据与计数，绝不含口令、密钥文件内容或恢复密钥材料；统计记录建槽、解锁成功、解锁失败与 rekey 次数。

硬约束：解锁计数与活动槽切换必须原子，同一槽位的并发解锁不能把失败次数写丢；建槽与删除槽同样串行化，配额判断与编号分配必须一次完成；快照在操作边界产生，恢复过程中不接受新的解锁或 rekey；重启后槽位编号水位、失败计数与活动槽必须与快照一致；快照带版本号，版本不认识时拒绝恢复；槽位总数与单槽失败计数有固定上限，超出按锁定处理而不是回绕。

验收：go test ./... 的 15 条用例全过，不得修改测试、跳过用例或按固定输入写死结果；并发与重启恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

模块路径 `gsb/batch48-go-safety-d-14`，包名 `task`，实现文件 `luks_keyslot_guard.go`，用例位于 `tests/`。
