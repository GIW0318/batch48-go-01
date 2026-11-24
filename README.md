# 对局归属租约、fencing 令牌与脑裂接管

## 题面

跨机房切换之后，同一场对局被两个实例同时写入，比分出现两份；被接管的旧实例还在往对局里写回滚前的状态，把新实例刚写好的战报覆盖掉了。事后回放又发现两处问题：旧实例的续期请求在新实例接管之后才到达，却把已经过期的租约又续活了；另一次进程重启之后令牌从 3 退回 1，旧实例凭旧令牌重新拿到了写权限。这一版要给每场对局只留一个写者，令牌只增不减，接管方必须先完成状态对齐才允许写入。落点 gsb/batch48-go-game-a-08 的 match_lease_guard.go，类型 MatchLeaseGuard，签名保持：NewMatchLeaseGuard(maxLeases int) *MatchLeaseGuard、Acquire(matchID, instanceID string, ttl time.Duration) (Lease, error)、Renew(matchID, instanceID string, fence uint64) error、Release(matchID, instanceID string) error、Takeover(matchID, instanceID string) (Lease, error)、Fence(matchID string) (uint64, error)、Apply(matchID, instanceID string, fence uint64, payload string) error、Align(matchID, instanceID string) error、Lease(matchID string) (Lease, bool)、Snapshot() ([]byte, error)、Restore([]byte) error。

要修：Acquire 首签令牌从 1 起，返回 State 为 LeaseActive、到期时间为当前时刻加 ttl 的租约，同一对局同一时刻只能有一个有效租约，重复申请报 ErrLeaseHeld，ttl 为零或负数报 ErrInvalidTTL，空对局号报 ErrInvalidMatchID，全空白的实例号报 ErrInvalidInstanceID，任何被拒绝的申请都不得在表里留下记录；续期必须同时匹配持有实例与令牌，令牌不符报 ErrFenceMismatch，非持有者报 ErrNotLeaseHolder；续期成功把到期时间顺延到当前时刻加租约时长，而不是在原到期时间上叠加；释放只允许持有者调用，非持有者报 ErrNotLeaseHolder 且租约原样保留，释放后 Lease 不再返回该租约，允许新实例申请且令牌加一；租约到期后允许更高令牌接管，接管令牌加一、状态先置为 LeaseAligning、到期时间按当前时刻加租约时长，未过期时接管报 ErrLeaseActive；Fence 返回该对局已签发的最大令牌，无记录报 ErrLeaseNotFound，写入请求按令牌单调性校验，低于或高于当前令牌都拒绝；Apply 只认当前持有实例与当前令牌，令牌不符报 ErrFenceMismatch，实例不符报 ErrNotLeaseHolder，二者都对才放行；旧实例被接管后仍发出带旧令牌的写入必须拒绝，StaleWrites 能查到累计次数；续期在租约刚过期而接管尚未完成的时间窗内按过期处理，不得把租约续活；接管方拿到令牌后必须先调用 Align 完成状态对齐才允许写入，未对齐前的写入报 ErrNotAligned，对齐期间到达的写入挂起而不是丢弃，HeldWrites 能查到挂起数量；租约表按 maxLeases 设上限，超出报 ErrLeaseTableFull 且不影响已有记录；快照保存令牌与到期时间，重启后令牌不得回退、到期时间不变，恢复后凭同一令牌仍能写入，坏快照报 ErrBadSnapshot 且不改动现有状态。

硬约束：同一对局同一时刻只有一个有效租约，续期与接管并发时以令牌大者为准，失败方必须拿到明确错误；令牌单调递增，重启、释放与接管都不得退回旧令牌；租约表与挂起写入都设硬顶，超出按错误返回而不是覆盖；所有到期判断走同一可替换时钟，注入时钟后结果必须可复现。

验收：go test ./... 15 条全过，测试不得修改、跳过或按固定输入写死；续期与接管并发、重启令牌回退自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-game-a-08`
- 实现文件：`match_lease_guard.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`MatchLeaseGuard`
