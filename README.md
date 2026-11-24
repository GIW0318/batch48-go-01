# batch48-go-safety-d-02

## 题面

密钥管理服务在季度审计时被问住：备份任务拿着导出用途的密钥，居然解开了生产数据密钥的明文，用途隔离写在信封上却没人校验；同一把数据密钥被两个团队反复包装，包装次数统计比实际少了一半，某一代 KEK 的配额早就耗尽却还在继续发信封。更麻烦的是一次批量重封做到一半时配额见底，前一半信封已经换了新代际，后一半还挂在旧代际上；服务重启后从快照恢复，剩余配额和熔断开关全部回到初始值，之前连续失败触发的熔断被静默解除。值班同学还发现，算法标识被改过的信封照样能解出明文，退役代际的历史信封仍混在查询结果里，审计队列永远是空的。落点 gsb/batch48-go-safety-d-02 的 envelope_key_wrap_guard.go，类型 KeyWrapGuard，按用途维护版本化密钥环，包装数据密钥时把用途、主体与 KEK 代际一起写进信封。构造后代际为 1、算法为 aes-256-gcm、每代默认配额 64；RegisterDEK 要求 key 至少 8 字节且 maxWraps 为正；Unwrap 先比对信封携带的用途，再按编号查记录，逐项校验后才还原明文。

接口保持：NewKeyWrapGuard(purpose Purpose) *KeyWrapGuard、RegisterDEK(id string, key []byte, maxWraps int) error、Wrap(dekID string, subject string) (Envelope, error)、Unwrap(env Envelope) ([]byte, error)、RotateKEK(alg Algorithm, quota int) (int, error)、ResealBatch(purpose Purpose) (ResealReport, error)、RetireGeneration(gen int) error、AuditQueue() []string、SetFailureThreshold(n int) error、BreakerOpen() bool、DEKStatus(id string) (DEKStatus, error)、QuotaRemaining() int、Snapshot() (KeyWrapSnapshot, error)、Restore(snap KeyWrapSnapshot) error、Stats() KeyWrapStats。Envelope 带 ID、DEKID、Subject、Purpose、Generation、Algorithm、Wrapped；Purpose 只接受 data、backup、export；Algorithm 只接受 aes-256-gcm 与 chacha20-poly1305；DEKStatus 带 Wraps、Generation、Algorithm、EnvelopeID；ResealReport 带 From、To、Resealed、Skipped。

要修的行为：（1）Wrap 不校验用途，把生产数据密钥包进导出用途的密钥环也不报错，空主体照样发信封。（2）同一把 DEK 的包装次数没有上限，超过 maxWraps 继续包装，DEKStatus 里的计数与实际信封数对不上。（3）每代 KEK 的配额不扣减，QuotaRemaining 永远返回初始值，配额耗尽仍继续发信封。（4）Unwrap 只比对信封编号，不校验用途、主体与算法三项绑定，改掉任意一项都能解出明文。（5）解封失败不累加熔断计数，连续被篡改的信封照样一次次尝试，达到阈值后 BreakerOpen 仍为 false。（6）熔断打开后合法信封仍被放行，阈值可以设成零或负数。（7）成功解封不清零失败连击，偶发一次错误之后整个服务永久熔断。（8）RotateKEK 允许代际跳号、允许算法为空，返回值与内部代际对不上。（9）批量重封不是原子的，配额不足时已重封的信封不回滚，重封计数照加。（10）退役代际被淘汰后，引用它的历史信封仍留在 DEKStatus 查询结果里，也不进审计队列。（11）快照只存 DEK 不存信封与配额用量，恢复后剩余配额、熔断状态与包装计数全部回弹。（12）并发包装同一把 DEK 时计数互相覆盖，出现重复信封编号。

硬约束：同一 DEK 的并发包装必须串行化，信封编号唯一且单调；批量重封要么全成要么全不动，失败零残留，配额不足时不扣任何额度；快照恢复后每代配额余量与熔断状态不得回弹，算法标识与代际必须来自同一份密钥环记录，熔断打开后连合法信封也一律拒绝。

验收：go test ./... 15 条全过，不得修改测试、不得跳过、不得按固定输入写死；并发重封与重启恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块路径与包名

- module: gsb/batch48-go-safety-d-02
- 根目录包名: task（envelope_key_wrap_guard.go）
- 测试目录: tests（package task）
- Go 1.23，仅标准库
