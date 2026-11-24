# 多租户 overlay 内层前缀登记与撤销

## 题面

多租户 overlay 隧道的内层前缀登记上线后连续出问题：甲租户先宣告 10.0.0.0/8，乙租户随后宣告 10.1.0.0/16 也能登记成功，数据面两个租户的报文互相串，整整一个可用区的业务被误路由到别人的隧道；运维在控制台撤销一个已经被父前缀覆盖的子前缀，本该继续命中的父前缀也跟着消失；两个租户之间临时开通的 transit 授权回收以后，原先靠它登记的重叠前缀仍然留在查询表里，直到进程重启才消失，期间数据面一直按旧授权转发。落点 gsb/batch48-go-safety-a-21 的 overlay_prefix_broker.go，类型 PrefixBroker，签名保持：NewPrefixBroker(limit int) *PrefixBroker、Register(tenant string, prefix netip.Prefix, attrs Attrs) error、Withdraw(tenant string, prefix netip.Prefix) error、Lookup(dst netip.Addr) (string, bool)、SetFilter(tenant string, f Filter) error、SetTransit(a, b string, allowed bool)、TransitAllowed(a, b string) bool、Count(tenant string) int、Snapshot() Snapshot、Restore(s Snapshot) error。

要修：租户名为空报 ErrBlankTenant，前缀无效报 ErrInvalidPrefix，IPv6 前缀与 IPv4 遵循同一套规则；带主机位的非规范前缀（例如 10.0.0.5/24）报 ErrNotCanonical，比较前必须先规范化；同租户重复登记同一前缀报 ErrDuplicatePrefix，同租户内互相包含报 ErrSameTenantOverlap，注册先后顺序不影响结论；跨租户完全相同或包含、又没有 transit 授权时报 ErrCrossTenantOverlap，授权只对这两个租户生效、不影响第三方；transit 开通后允许重叠，查询按最长前缀命中，长度相同时按租户名字典序取小，落在所有前缀之外的地址返回未命中；每租户前缀数有硬上限，超限报 ErrPrefixLimit 且现有表一项都不改动，也不能只写一半；撤销未登记前缀报 ErrUnknownPrefix，撤销不存在的租户报 ErrUnknownTenant；撤销必须原子地同时影响查询表与计数，撤销一个被父前缀覆盖的子前缀时父前缀必须继续命中；过滤器 Allow/Deny 之外的登记报 ErrFiltered，过滤器里出现非法前缀报 ErrBadFilter，过滤器变更后立即影响查询，但已登记前缀的计数不变；transit 授权解除后，依赖它注册的重叠前缀要批量标记不可用并给出清单，不能静默保留，也不能把它们从计数里悄悄删掉；快照恢复必须重新执行跨租户重叠校验，不能直接信任快照里的有效标记，只恢复部分租户导致跨租户重叠校验失败时整次恢复失败、旧表保持原样；快照版本不符报 ErrBadSnapshot。

硬约束：查询走读锁或原子快照，注册、撤销、过滤器与授权变更串行，读方不得看见半更新的表，也不能读到仍按旧授权生效的条目；前缀表、过滤器与授权关系要能快照并在重启后恢复，恢复过程要么整体成功要么完全不生效；每租户前缀数按上限封顶，超限一律拒绝而不是部分写入。

验收：go test ./... 15 条全过，不得改测试、跳过或用固定输入写死；并发与重启恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-a-21`
- 实现文件：`overlay_prefix_broker.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`PrefixBroker`
