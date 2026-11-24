# 隧道侧路由重分发、split horizon 与策略变更的原子发布

## 题面

隧道控制器把本地直连与静态路由重分发进数据面后，运维遇到三类问题：两个边界设备宣告了同一条 /24，主设备维护时被撤销，查询表在整整三秒里查不到这条前缀，流量全部黑洞；一次策略变更把某个前缀过滤掉后，来源记录被顺手清掉，策略回滚时只能重新导入，导入期间同前缀的其他来源又被误删；控制器重启恢复旧快照后，一个已经撤销的来源重新出现在发布表里，数据面按幽灵路由转发了半小时。落点 gsb/batch48-go-safety-a-30 的 tunnel_route_redistributor.go，类型 RouteRedistributor，签名保持：NewRouteRedistributor(p Policy) *RouteRedistributor、AddSource(kind SourceKind, routes []Route) error、WithdrawSource(kind SourceKind, id string) error、SetPolicy(p Policy) error、Rebuild() ([]Route, error)、Lookup(dst netip.Addr) (Route, bool)、Sources() []SourceInfo、Stats() RedistributorStats、Snapshot() ([]byte, error)、Restore(data []byte) error。来源类型为 SourceConnected、SourceStatic、SourceTunnel。

要修：来源类型未知报 ErrBadKind，来源号为空、前缀无效、下一跳无效、度量为负都报 ErrBadRoute；同一来源号重复导入时整源替换并把版本号加一，不能残留旧前缀；同前缀多来源先比有效度量（路由度量加该类型的 BaseMetric），再比 Priority 小的类型优先，仍相同按来源号字典序取小，结果必须确定；撤销未知来源报 ErrSourceNotFound，来源号存在但类型不符报 ErrKindMismatch；撤销必须一次性移除该来源全部派生路由，不得误删其他来源的同前缀路由，其他来源的同前缀路由要立即接替，不能出现短暂黑洞；每个前缀最多发布一条路由，发布表按前缀字符串升序排列；Sources 按来源类型与来源号升序返回，便于逐项核对版本；Policy 的 DenyPrefixes 命中前缀时该派生路由立即从发布表消失，但来源记录与版本号保留，策略改回后无需重新导入即可恢复；MaxMetric 小于等于零表示不设上限，大于零时有效度量超限的路由被过滤；split horizon 打开时，从隧道来源学到的前缀不得出现在宣告回该隧道的 AdvertiseTo 列表里，其他来源照常发给全部目标；SetPolicy 与 AddSource、WithdrawSource 一样，先完整重建再原子发布，读方通过 Lookup 只能看到完整表，不得看到增量半成品；Rebuild 返回完整发布快照；Stats 的 Sources 是来源数、Published 是当前发布表路由条数、Version 是发布版本号；快照保存来源、版本号、策略与发布版本，恢复后重新派生并校验，来源版本与派生路由必须一致，旧版本快照不得复活已撤销来源；Restore 对损坏数据报 ErrBadSnapshot，对版本回退的快照报 ErrStaleSnapshot。

硬约束：写路径（导入、撤销、策略、重建）串行执行，读路径只读取原子发布的整表快照，并发 Lookup 不能读到空表或半表；发布版本单调递增，快照与恢复要么整体生效要么完全不生效；同一前缀的选路在相同输入下必须给出相同结果，不得依赖 map 遍历顺序。

验收：go test ./... 15 条全过，不得改测试、跳过或按固定输入写死；并发与重启恢复自行验证；只做本地实现。
以上现象均在真实生产流量下复现。
## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-a-30`
- 实现文件：`tunnel_route_redistributor.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`RouteRedistributor`
