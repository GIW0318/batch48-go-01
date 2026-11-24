# batch48-go-safety-d-01

## 题面

运维值班接到投诉：某租户的密码找回流程在凌晨批量失败，恢复码显示无效，后台查不到错误记录；同一时间另一批用户的口令摘要被别家租户的登录请求命中，两个租户的槽位像是串在了一起。安全组复盘发现，上周的密钥轮换没有落到所有槽位，部分槽位仍用旧代际的封装记录解封；被连续输错而锁定的账号在轮换后失败计数归零；恢复码用光的账号没有自动进入锁定状态，管理员解锁后重新发码，旧的消耗计数被直接覆盖。并发写同一槽位会丢记录，重启后从快照恢复，代际号回到轮换前，已消耗的恢复码又能再用一次。落点 gsb/batch48-go-safety-d-01 的 secrets_slot_vault.go，类型 SecretSlotVault，按租户、用户与槽位名维护口令摘要、TOTP 种子与恢复码集合，每个槽位用代际化 KEK 封装成密封记录，构造后代际为 1、KEK 标识为 kek-1、算法为 aes-256-gcm，槽位在 active、locked、retired 三态间迁移，旧代际记录默认保留 2 次宽限，用完必须拒绝解封并要求先完成重封。

接口保持：NewSecretSlotVault(maxSlots int, maxFailures int) *SecretSlotVault、PutSlot(spec SlotSpec) error、Seal(tenant, user, name string) (SealedRecord, error)、Record(tenant, user, name string) (SealedRecord, error)、Open(rec SealedRecord, secret []byte) error、ConsumeRecovery(tenant, user, code string) (bool, error)、RotateKEK(nextGeneration int, kekID string) (RotateReport, error)、Unlock(tenant, user, name string) error、RegenRecovery(tenant, user string, codes []string) error、Retire(tenant, user, name string) error、Snapshot() (VaultSnapshot, error)、Restore(snap VaultSnapshot) error、Stats() VaultStats、Generation() int。SlotSpec 带 Tenant、User、Name、Kind、Secret、Codes；Kind 只接受 password、totp、recovery，recovery 槽位以 Codes 作为封装材料；SealedRecord 带 Generation、KEKID、Algorithm、Tag；SlotStatus 带 State、Generation、Failures、Consumed、Remaining、Grace；RotateReport 带 FromGeneration、ToGeneration、Resealed、Skipped。

要修的行为：（1）重复注册同名槽位不报错，覆盖原有密文与消耗计数；未知种类与空租户也照单全收。（2）槽位上限形同虚设，超过 maxSlots 仍扩容；既没有口令也没有恢复码的槽位也能 Seal，产出标签为零的记录。（3）Open 不校验记录与槽位的绑定，篡改代际或标签仍能解封；失败次数只写日志不写回槽位。（4）连续输错达到 maxFailures 的那一次仍返回普通不匹配错误，调用方无法判断槽位是否已锁定。（5）正确口令解封后失败计数不清零，第二天正常登录也被挡住。（6）同一恢复码可以反复使用，消耗计数被重复累加；不存在的恢复码返回成功。（7）轮换只改全局代际号不重封槽位，记录里的代际与 KEK 标识仍是旧的。（8）轮换允许跳号或原地重复，中途失败不回滚，留下半新半旧的记录。（9）批量重封跳过锁定槽位，锁定状态与失败计数随之丢失。（10）恢复码耗尽后槽位不会自动转 locked；管理员解锁再发码时，旧的消耗计数被清零。（11）旧代际宽限期用完后仍用旧 KEK 继续解封，从不要求先完成轮换。（12）快照恢复后代际、失败与消耗计数回到默认值；退役槽位仍能被 Seal 和 Open。

硬约束：同一租户下并发 PutSlot、ConsumeRecovery 与 Open 必须串行完成，计数不得丢失，快照与轮换期间不得读到半新半旧状态；轮换只能加一，且要求所有未退役槽位都已密封，否则整批中止并零残留，重封不得改动任何槽位的失败计数与锁定状态，RotateReport 的 Resealed 统计本轮重封的槽位（含 locked），Skipped 统计退役跳过的槽位；快照必须携带密封载荷与消耗状态，恢复后代际、失败与消耗计数与轮换前一致，解锁只清失败计数。

验收：go test ./... 15 条全过，不得修改测试、不得跳过、不得按固定输入写死；并发与重启场景自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块路径与包名

- module: gsb/batch48-go-safety-d-01
- 根目录包名: task（secrets_slot_vault.go）
- 测试目录: tests（package task）
- Go 1.23，仅标准库
