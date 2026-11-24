# 证书绑定访问令牌的指纹校验与绑定迁移治理

## 题面

证书绑定令牌上线后，风控组连着遇到两件事：同一张合法证书换了叶证书后还能拿旧令牌刷接口；多证书并存时令牌会串用。落点 gsb/batch48-go-safety-a-14 的 token_cert_binding_guard.go，类型 TokenCertBindingGuard。签名保持：NewTokenCertBindingGuard(CertLimits) *TokenCertBindingGuard、Issue(clientID, certFingerprint string, ttl time.Duration) (Token, error)、Bind(tokenID, certFingerprint string) error、VerifyPresented(rawToken, presentedFingerprint string, now time.Time) (Verdict, error)、Rebind(tokenID, newFingerprint string, reauthProof Proof) error、Snapshot() ([]byte, error)。

要修：空客户端号报 ErrBlankClient，空指纹报 ErrBlankFingerprint，非 64 位十六进制报 ErrBadFingerprint；指纹按小写去冒号归一并存入绑定；寿命非正报 ErrInvalidTTL，超上限截断；单客户端活跃绑定数超上限报 ErrBindingLimit；未绑定令牌出示报 ErrTokenUnbound；已绑定令牌再绑报 ErrAlreadyBound；未知令牌报 ErrUnknownToken，格式错误报 ErrMalformedToken；过期令牌报 ErrTokenExpired；证书链合法但叶证书指纹与绑定不符必须拒绝，不能因同一签发机构放行；绑定迁移只允许在重新认证成功后发生，普通刷新流程不得改绑；同一客户端多张证书并存时，每个绑定的令牌只能被其对应证书使用；撤销绑定后令牌不得再出示。

硬约束：迁移与使用并发时，读方只能看到旧绑定或新绑定；绑定表与序号要能快照并在重启后原样恢复；寿命与绑定数按配置上限封顶。

验收：go test ./... 15 条全过，测试不得修改、跳过、写死；并发与重启自行验证；只改本地。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-a-14`
- 实现文件：`token_cert_binding_guard.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`TokenCertBindingGuard`
