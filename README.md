# Go 实现题

## 题面

投诉：连接池把一条已钉扎的长连接交给另一租户复用，请求带着上一个租户的身份被放行；证书轮换中途新到请求被静默按新身份处理。落点 gsb/batch48-go-safety-a-09 的 peer_identity_pinner.go，类型 PeerIdentityPinner，包名 task。

签名保持：NewPeerIdentityPinner(opts PinOptions) *PeerIdentityPinner、Pin(connID, principal, certFingerprint string) (Pin, error)、Check(connID string) (Pin, error)、Authorize(connID, principal string) (bool, error)、Rotate(connID, newPrincipal, newFingerprint string) (uint64, error)、Commit(connID string, generation uint64) error、Drain(connID string, deadline time.Time) error、Snapshot() ([]byte, error)、Restore(data []byte) error。

要修：指纹入库前去分隔符转小写，长度与字符集不合规拒绝；首请求钉扎记录主体、指纹、代际号与时刻，同连接重复钉扎报错；授权只认当前钉扎身份，主体不符判否，未知连接报错；重协商只登记下一代身份并返回新代际号，同连接同时只允许一次；提交必须带当前代际号，不符拒绝且不改现有钉扎；重协商期间新到请求按旧身份处理并与排空截止对齐，不得静默按新身份放行；排空截止后拒绝新的在途请求，已登记的在途请求必须能完成；单连接在途请求数有上限，多减一次报错；主体大小写或转义形式不同但指纹相同视为同一身份，主体相同指纹不同必须视为不同身份；连接被池回收复用时清掉钉扎身份，不得把上一个租户的身份带进新租户；连接数与快照代际号可读。

硬约束：同一连接并发钉扎只能成功一次，并发重协商只能有一个赢家；快照保留钉扎、代际号与在途计数，重启后按快照重建而不是重新握手。验收：go test ./... 15 条全过，测试不得修改、跳过、写死；并发与重启自行验证；只做本地实现。

## 运行方式

```bash
cd batch48-go-safety-a-09
go test ./...
```

## 模块信息

- module: gsb/batch48-go-safety-a-09
- 源文件: peer_identity_pinner.go
- 包名: task
- 测试目录: tests/
