# 身份令牌声明与会话绑定的校验治理

## 题面

登录后台这两周收到同类工单：手机端登录后，桌面端刚开的会话被莫名踢掉；改过授权应用配置，本该失效的旧令牌还能换新会话。落点 gsb/batch48-go-safety-a-11 的 id_token_claim_binder.go，类型 IdTokenClaimBinder。签名保持：NewIdTokenClaimBinder(BinderConfig) *IdTokenClaimBinder、Bind(sessionID string, claims Claims) (Binding, error)、Validate(sessionID, rawIDToken string, now time.Time) (Result, error)、ConsumeNonce(sessionID, nonce string) error、NeedsReauth(sessionID string, now time.Time) (bool, string)、Snapshot() ([]byte, error)、Restore(data []byte) error。

要修：空会话号或空主体报 ErrBlankIdentity；受众为空报 ErrNoAudience，超限报 ErrAudienceLimit；单用户会话数超上限报 ErrSessionLimit；令牌受众越出绑定受众报 ErrAudienceMismatch；多受众令牌只在授权方等于本客户端时接受，缺授权方标识必须拒绝；nonce 只能用一次，重复报 ErrNonceReused，未签发报 ErrNonceUnknown；认证时刻缺失不得用签发时间顶替，报 ErrAuthTimeMissing；认证时刻早于最长时效报 ErrAuthAgeExceeded；时钟偏差只取单侧容差，本地时钟超前认证时刻超过容差报 ErrClockAhead；配对摘要不一致报 ErrTokenPairMismatch；需要重认证时要给出原因串。

硬约束：同一会话的 nonce 消费必须原子，并发校验只放行一路；绑定关系、已消费 nonce 与会话序号要能快照并重启后恢复，恢复后新建会话序号大于此前任一会话；单用户会话条目按上限封顶。

验收：go test ./... 15 条全过，测试不得修改、跳过、写死；并发与重启自行验证；只改本地。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-a-11`
- 实现文件：`id_token_claim_binder.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`IdTokenClaimBinder`
