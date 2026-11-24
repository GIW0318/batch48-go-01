# Go 实现题

## 题面

投诉：同一个浏览器新开标签页从第二家提供方重新登录，回跳后拿到的却是第一家提供方的账号；另有用户点两次授权，回调被重放，会话被解绑。落点 gsb/batch48-go-safety-a-07 的 idp_mixup_guard.go，类型 IdpMixupGuard，包名 task。

签名保持：NewIdpMixupGuard(opts GuardOptions) *IdpMixupGuard、RegisterIdP(idpID, issuer, clientID string) error、Begin(sessionID, idpID, redirectURI string) (Request, error)、Callback(state, issuer, code string) (Result, error)、Abandon(state string) error、Pending(sessionID string) int、Completed() int、IssuerFor(state string) (string, error)、Snapshot() ([]byte, error)、Restore(data []byte) error。

要修：每次发起生成独立悬挂请求，记录 state、发起方、回跳地址与过期时刻；回调按 state 找回发起方，再拿注册 issuer 与回调 issuer 比对，不一致报错且不消费；同一会话两个标签页向不同提供方发起时回调各回各家，不得复用 state；issuer 尾部斜杠按归一化比较，发现文档里的 issuer 不得被请求参数覆盖；回调只能原子消费一次，重复回调要区分已消费与未知 state；过期请求报过期并清出悬挂表；空 code 视为无效且请求继续悬挂；悬挂表按会话设上限，超限拒绝；用户改选另一个提供方重试时旧请求在新请求登记成功后才作废；未知提供方、非 https 回跳、重复注册都报错；快照跨重启保留未完成请求与原过期时间。

硬约束：并发回调同一 state 只允许一次成功，其余拿到明确错误；悬挂表读写经受多协程同时发起与回调。验收：go test ./... 15 条全过，测试不得修改、跳过、写死；并发与重启自行验证；只做本地实现。

## 运行方式

```bash
cd batch48-go-safety-a-07
go test ./...
```

## 模块信息

- module: gsb/batch48-go-safety-a-07
- 源文件: idp_mixup_guard.go
- 包名: task
- 测试目录: tests/
