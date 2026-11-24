# 多因子登记台账与认证仪式状态机

## 题面

值班反馈：连错三次口令也没锁号，因子移除后旧仪式还能换令牌。落点 gsb/batch48-go-safety-a-02 的 mfa_ceremony_guard.go（MfaCeremonyGuard），签名保持：NewMfaCeremonyGuard(GuardConfig) *MfaCeremonyGuard、EnrollFactor(userID, factorID, kind string) (Factor, error)、BeginCeremony(userID, sessionID string) (Ceremony, error)、VerifyFactor(ceremonyID, factorID, proof string) (Verdict, error)、RedeemCeremony(ceremonyID string) (Grant, error)、Snapshot() ([]byte, error)、Restore([]byte) error；配置里的各项上限、窗口与 ProofVerifier、Now 都要生效。

要修：新登记因子直接可用，须 pending 后确认成 active，重复确认报 ErrFactorState；未知类型报 ErrFactorKind，空白账号或因子号报 ErrBlankArg；重复登记报 ErrFactorExists，超上限报 ErrFactorLimit；同账号第二个仪式报 ErrCeremonyInFlight；因子数不足发令牌须报 ErrCeremonyIncomplete；假 proof 须拒绝并给 reason_proof_mismatch；同一因子在一次仪式内重复验证只计一次；已移除因子不得再参与验证，但之前签发的令牌在有效期内仍可用完；知识类失败计入锁定，持有类读取失败不计入但单独计数；失败达上限须按锁定窗口锁号，LockRemaining 如实返回剩余；令牌可反复兑换，再兑须报 ErrCeremonyConsumed；重启后仪式与锁定剩余须由快照恢复延续。

硬约束：并发开仪式只留一个挑战；快照只见全量旧或新状态；坏快照 Restore 报 ErrBadSnapshot 不动现网。

验收：go test ./... 15 条全过，测试不得修改、跳过、写死；并发与重启恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-a-02`
- 实现文件：`mfa_ceremony_guard.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`MfaCeremonyGuard`
