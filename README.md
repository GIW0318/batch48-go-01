# 客户端密钥双活轮换与单密钥吊销治理

## 题面

密钥轮换上线后，集成方连夜接到两次告警：轮换刚完成旧密钥就失效，对账任务全部 401；同一客户端并行轮换时，两对新密钥同时生效，配置中心出现三代密钥。落点 gsb/batch48-go-safety-a-12 的 client_secret_overlap_guard.go，类型 ClientSecretOverlapGuard。签名保持：NewClientSecretOverlapGuard(SecretLimits) *ClientSecretOverlapGuard、Register(clientID string, now time.Time) (Generation, error)、Rotate(clientID string, now time.Time) (Generation, error)、Activate(clientID string, gen int) error、Verify(clientID, secret string, now time.Time) (Verdict, error)、RevokeOne(clientID string, gen int) error、Expire(now time.Time) int、Generations(clientID string) []Generation、Snapshot() ([]byte, error)、Restore(data []byte) error。

要修：空客户端号报 ErrBlankClient，重复登记报 ErrClientExists；未登记客户端轮换报 ErrClientNotFound；轮换产出下一代，旧代留重叠窗口；同一请求重放只产出一代；并发轮换只允许一个成功；可用密钥不超过两代，同一客户端连续轮换时第一代必须在新一代登记后立刻退出可用集合，不得三代并存；窗口未到期就手动作废旧密钥时，仍在用旧密钥的调用方要拿到可区分的错误码，而非通用鉴权失败；验证失败要区分代际过期、已吊销与查无此密钥；激活未知代号报错；到期清理只回收过期代。

硬约束：到期清理与验证并发时，读方只能看到完整一代集合；轮换、验证与快照互斥，快照不得半更新；代际表与过期水位跨重启恢复。

验收：go test ./... 15 条全过，测试不得修改、跳过、写死；并发与重启自行验证；只改本地。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-a-12`
- 实现文件：`client_secret_overlap_guard.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`ClientSecretOverlapGuard`
