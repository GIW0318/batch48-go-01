# QUIC 连接 ID 发放、退役宽限与 DCID 路由表治理

## 题面

边缘网关的 QUIC 服务在上线连接迁移后连续出了三起事故：客户端迁移到新地址后，旧路径的在途包被判成未知连接直接丢弃，业务侧表现为半秒断流；一次批量轮换把 retire_prior_to 推到尚未发放的序号，接口返回成功，但一半连接的旧 CID 已经先失效，重传全部失败；某条零长度 CID 的长连接被反复申请新 CID，路由表里堆了几千条无主表项，最后把内存打满。故障复现时还发现：同一批 CID 在并发申请下偶尔出现重复，路由表最后一条覆盖前一条，两个连接共用同一个 DCID；退役后的活动集合与路由表不是同一时刻更新，监控看到活动数已归零但旧 CID 还在转发；默认桶里的零长度连接在快照恢复后从列表里消失，业务方无法再按连接号找到它们；统计口径也不一致，活动 CID 数量与实际可路由数量对不上。落点 gsb/batch48-go-safety-a-26 的 quic_cid_rotation_ledger.go，类型 CIDRotationLedger，签名保持：NewCIDRotationLedger(lim CIDLimits) *CIDRotationLedger、Open(connID string, zeroLength bool) error、Issue(connID string, n int) ([]CID, error)、Claim(connID string, dcid CID) error、Retire(connID string, priorTo uint64, now time.Time) error、Route(dcid CID) (string, bool)、Release(connID string, now time.Time) error、Sweep(now time.Time) int、ActiveCIDs(connID string) []CID、RetiredCIDs(connID string) []CID、DefaultConnections() []string、Stats() LedgerStats、Snapshot() ([]byte, error)、Restore(data []byte) error。

要修：空连接号报 ErrBlankConn，重复 Open 报 ErrConnExists；未登记连接调用 Issue、Retire、Claim、Release 一律报 ErrConnNotFound；Issue 的 n 小于等于零报 ErrBadCount，超过每连接活动 CID 上限报 ErrActiveLimit 且一个都不发，不能只发一半；零长度 CID 连接不得发放或认领新 CID，统一报 ErrZeroLengthCID，只能出现在 DefaultConnections 的默认桶里；生成与认领的 CID 必须全局唯一，同一 CID 不能同时出现在两条连接上，认领已被其他连接占用的 CID 报 ErrCIDInUse，原归属保持不变，后申请者不得覆盖；retire_prior_to 只能单调不减，回退报 ErrRetireBackwards，超过已发放最大序号报 ErrRetireBeyondIssued 且整次轮换不生效；被退役的 CID 在宽限期内仍按原连接路由，宽限期结束后由 Sweep 回收，Sweep 返回回收条数；退役后活动 CID 列表与在途路由必须同时可见，不能出现活动集合已删、路由还在的半更新；连接释放后不得再发放新 CID，重复释放报 ErrConnReleased，释放时仍被在途包引用的 CID 要等宽限期结束再回收；快照要保留已退役 CID 及其宽限截止时间，恢复后在途包仍能路由到原连接；Restore 拒绝空数据与损坏内容，报 ErrBadSnapshot；Route 对未知 CID 返回空连接号与 false。

硬约束：Issue、Claim、Retire、Release、Sweep 与 Route 共用读写锁，路由表与每连接 CID 集合的更新必须原子，并发 Issue 不能发出重复 CID，读方不能看到半张路由表；同一 CID 的归属一旦建立，任何路径都不得覆盖；快照与恢复要么整体生效要么完全不生效，恢复后的宽限截止时间按原值继续计算。

验收：go test ./... 15 条全过，不得改测试、跳过或按固定输入写死；并发与重启恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-a-26`
- 实现文件：`quic_cid_rotation_ledger.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`CIDRotationLedger`
