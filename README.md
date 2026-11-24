# batch48-go-safety-d-04

## 题面

网格升级到新的授权引擎后，支付域的调用开始随机被拒：同一个 principal 访问同一个工作负载，十分钟内一会儿放行一会儿拒绝，重试又好了。安全团队复核策略发现，两条策略同时命中时 allow 有时压过 deny；通配路径命中多条规则时，最终生效的是哪一条完全看进程启动时 map 的遍历顺序，两个副本的裁决对不上。策略热更新之后，旧的放行决定还留在缓存里，被撤销的 principal 继续通行；某个连接因为重试风暴打满了请求配额，其余连接跟着一起被限流。跨副本比对时还发现，批量替换策略的瞬间飞行中的请求读到了半新半旧的规则集，同一请求在快照里出现两个不同的裁决代际。落点 gsb/batch48-go-safety-d-04 的 service_mesh_authz_guard.go，类型 MeshAuthzGuard，在数据面按 principal、目标工作负载与 L7 方法路径匹配策略，deny 永远先于 allow，未命中落到命名空间默认动作。

接口保持：NewMeshAuthzGuard(defaultEffect Effect) *MeshAuthzGuard、ApplyPolicySet(set PolicySet) (ApplyReport, error)、Authorize(req AuthRequest) (Decision, error)、Invalidate(key CacheKey) int、SetDefaultAction(namespace string, eff Effect) error、SetConnQuota(conn string, quota int) error、ConnUsage(conn string) int、SetCacheCapacity(n int) error、CacheSize() int、Generation() int、PolicyCount() int、Snapshot() (AuthzSnapshot, error)、Restore(snap AuthzSnapshot) error、Stats() AuthzStats。Policy 带 ID、Principal、Namespace、Workload、Method、PathPrefix、Effect、Order；PolicySet 带 Generation 与 Policies；AuthRequest 带 ConnectionID、Principal、Namespace、Workload、Method、Path；Decision 带 Effect、PolicyID、Generation、Cached、Reason；CacheKey 与求值输入字段一一对应；空白策略集也是合法的一代，落到命名空间默认或全局默认的裁决不带策略编号，Stats 累计求值、放行、拒绝与缓存命中。

要修的行为：（1）ApplyPolicySet 允许代际重复或跳号，旧策略集能把新策略覆盖回去。（2）策略 ID 重复、效果值写成 permit、路径前缀为空都照单全收，错误留到运行期才炸。（3）deny 与 allow 同时命中时按加入顺序取第一条，allow 有机会压过 deny。（4）通配前缀命中多条规则时按 map 遍历顺序取第一条，同一请求在两个副本得到不同裁决。（5）未命中策略时直接拒绝，命名空间默认动作形同虚设。（6）每连接配额是全局共享的，一个连接打满之后所有连接一起被拒。（7）配额耗尽返回空裁决而不是明确错误，调用方以为这是策略拒绝。（8）缓存命中不标记，统计里 CacheHits 永远为零，无法判断缓存是否生效。（9）Invalidate 只清一层前缀，同一请求换个路径又命中旧裁决。（10）缓存容量没有上限，策略一多就把进程内存吃满。（11）策略更新后旧裁决继续生效，被撤销的 principal 还能通行；无法判断相关性时也不整体清空缓存。（12）批量替换时飞行中的求值读到半新半旧的规则集；快照恢复后代际、默认动作与计数丢失。

硬约束：策略代际单调递增，批量替换要么整代生效要么完全不动；并发求值必须按请求进入时的代际快照完成裁决，不得读到半新半旧的规则集；缓存容量与每连接配额都是硬顶，超限返回可识别错误而不是静默扩张，缓存键无法判定相关性时必须整体失效。

验收：go test ./... 15 条全过，不得修改测试、不得跳过、不得按固定输入写死；并发求值与策略热更新自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块路径与包名

- module: gsb/batch48-go-safety-d-04
- 根目录包名: task（service_mesh_authz_guard.go）
- 测试目录: tests（package task）
- Go 1.23，仅标准库
