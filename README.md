# 租户内主体标识归一化与保留键冲突治理

## 题面

客服本月连报两次冒名事件：有人在用户名里塞全角字母和零宽字符，注册出与老用户看起来一样的标识；另有代理商把邮箱域名改掉大小写后重复占用同一个账号名。落点 gsb/batch48-go-safety-a-13 的 subject_canonicalizer.go，类型 SubjectCanonicalizer。签名保持：NewSubjectCanonicalizer(CanonRules) *SubjectCanonicalizer、Canonicalize(tenantID, kind, raw string) (Key, error)、Reserve(tenantID, key, accountID string) (bool, error)、Release(tenantID, key string) error、Lookup(tenantID, key string) (string, bool)、Conflicts(tenantID, kind, raw string) ([]Conflict, error)、History(tenantID, key string) []Reservation、ReserveCount(tenantID string) int、Snapshot() ([]byte, error)、Restore(data []byte) error。

要修：空租户报 ErrBlankTenant，未知主体类型报 ErrUnknownKind，超长标识报 ErrKeyTooLong；归一化覆盖大小写折叠、全角转半角与不可见字符清理；归一化后为空的标识必须拒绝，不得落到某个默认键上；邮箱本地部分大小写敏感、域名部分不敏感，两侧不能套同一套规则；冲突结果要指出差异位置与前后字符；保留表按租户隔离并设上限，超限报 ErrReservationLimit；并发保留同一键只允许一个成功；重复保留同一键返回未新增；释放后可重新占用，重复释放幂等；保留键释放后短时间内的重新占用要留历史，便于识别抢注与冒名。

硬约束：保留与释放串行裁决，同一键不会出现两个属主；保留表与历史要能快照并在重启后原样恢复；单租户保留条目与单键历史条目都按配置上限裁剪。

验收：go test ./... 15 条全过，测试不得修改、跳过、写死；并发与重启自行验证；只改本地。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-a-13`
- 实现文件：`subject_canonicalizer.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`SubjectCanonicalizer`
