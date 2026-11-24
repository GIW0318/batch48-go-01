# 一次性邀请与重置令牌的哈希落库与原子兑换

## 题面

审计发现邀请令牌明文落库、被兑换两次。落点 gsb/batch48-go-safety-a-03 的 one_time_token_vault.go（OneTimeTokenVault），签名：NewOneTimeTokenVault(VaultConfig) *OneTimeTokenVault、Issue(tenantID, purpose string, ttl time.Duration) (Token, error)、IssueBatch(tenantID, purpose string, n int) ([]Token, error)、Redeem(tenantID, purpose, token string) (Subject, error)、Revoke(tokenID string) error、Snapshot() ([]byte, error)、Restore([]byte) error。

要修：明文落库须只留哈希与用途标签，原串只在签发时回给调用方；兑换不原子，并发只成功一次，其余报 ErrTokenConsumed；过期仍可兑换须报 ErrTokenExpired 并落成 expired，过期与已兑换记录常驻不回收，Purge 按时间回收条目不随兑换增长；跨租户或用途不符的兑换放行须报 ErrTenantMismatch、ErrPurposeMismatch，用途不符计入失败次数、达上限提前作废；篡改 secret 报 ErrTokenMismatch 不消费令牌；批量签发须全有或全无，超上限报 ErrBatchTooLarge，n 非正报 ErrInvalidCount；跨批次配额会借用上一批余量，须按租户与用途累计封顶报 ErrQuotaExceeded；ttl 非正或租户用途空白报 ErrInvalidTTL、ErrBlankArg；撤销后仍可兑换须报 ErrTokenRevoked，重复撤销幂等，未知名报 ErrTokenNotFound；恢复后已兑换令牌不得复活；确认回调失败后令牌不得退回，须作废重签。

硬约束：并发兑换只成功一次；快照只见全量旧或新，坏快照报 ErrBadSnapshot 不动现网；配额与条目数硬顶。

验收：go test ./... 15 条全过，测试不得改动、跳过、写死；并发与重启自验；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-a-03`
- 实现文件：`one_time_token_vault.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`OneTimeTokenVault`
