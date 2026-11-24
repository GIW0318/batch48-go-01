# Go 实现题

## 题面

投诉：两个彼此独立的合作伙伴客户端拿到了能互相换算的用户标识；另一家子公司换回调域名后老用户的历史标识全部失联。落点 gsb/batch48-go-safety-a-06 的 pairwise_subject_guard.go，类型 PairwiseSubjectGuard，包名 task。

签名保持：NewPairwiseSubjectGuard(opts Options) *PairwiseSubjectGuard、RegisterSector(tenantID, sectorID, uri string) error、AssignClient(tenantID, clientID, sectorID string) (Sector, error)、SubjectFor(tenantID, clientID, localUserID string) (string, error)、MoveClient(tenantID, clientID, newSector string) (Remap, error)、Tombstone(tenantID, subject string) error、Resolve(tenantID, subject string) (string, bool)、Snapshot() ([]byte, error)、Restore(data []byte) error。

要修：扇区地址按大小写、默认端口、尾斜杠归一化后归并，同租户内不得重复登记；同扇区不同客户端对同一用户必须同标识，跨扇区必须不同；标识由扇区密钥派生、可复现且不含本地用户明文；客户端未登记扇区时按独立扇区处理，不得继承租户默认扇区而与其它客户端串号；换扇区为已签发用户生成新旧对照并记墓碑；换扇区后旧标识只用于过渡期映射查询，不得再签发给新会话；同一扇区地址被两个租户登记互不影响，密钥按租户隔离；密钥轮换不改变已签发标识且轮换次数可读；租户扇区数与单扇区客户端数有硬上限；移到未登记扇区报错并保持原分配。

硬约束：并发 SubjectFor、AssignClient、MoveClient 时同一用户只能得一个标识；快照恢复后扇区、客户端、标识与墓碑逐字节一致。验收：go test ./... 15 条全过，测试不得修改、跳过、写死；并发与重启自行验证；只做本地实现。

## 运行方式

```bash
cd batch48-go-safety-a-06
go test ./...
```

## 模块信息

- module: gsb/batch48-go-safety-a-06
- 源文件: pairwise_subject_guard.go
- 包名: task
- 测试目录: tests/
