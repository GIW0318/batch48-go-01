# DNS 查询单飞合并、失败分级缓存与客户端配额隔离

## 题面

解析器前端的 DNS 合并缓存上线后，值班连续收到三类反馈：同一域名被 A 与 AAAA 两种查询同时打进来，上游只回了一个 NXDOMAIN，另一类查询随后也被直接判成不存在，持续了整整一个负缓存周期；一次上游超时后，所有等待的客户端都收到超时，但结果其实在几秒后才回来，这段时间同类查询又把上游打穿；某个爬虫客户端把配额打满后，它的请求仍然挂在单飞组里占着位置，正常客户端的查询跟着一起变慢。回放还发现：缓存条目没有任何上限，热点域名高峰把进程内存吃满；同一 qname 只是大小写不同就被当成两个键，命中率惨淡；SERVFAIL 被按普通成功的长 TTL 缓存了十分钟，上游恢复后十分钟内仍返回旧错误。落点 gsb/batch48-go-safety-a-28 的 dns_query_deduper.go，类型 QueryDeduper，签名保持：NewQueryDeduper(upstream UpstreamFunc, opts DeduperOptions) *QueryDeduper、Resolve(q Question, clientID string, now time.Time) (Answer, error)、Invalidate(qname string)、SetQuota(clientID string, qps float64, burst int) error、Sweep(now time.Time) int、CacheSize() int、Stats() DeduperStats、Quota(clientID string) (float64, int, bool)、Snapshot() ([]byte, error)、Restore(data []byte) error。Question 带 Name 与 Type，Answer 带 RCode、Records 与 TTL。

要修：空 qname 或零 qtype 报 ErrBadQuestion，空客户端号报 ErrBadClient；相同 qname 与 qtype 的并发请求只允许一次上游调用，等待者共享同一个 Answer；缓存键必须带 qtype，不能只按 qname 合并；成功应答的 TTL 取所有记录里的最小值，ExpiresAt 按该 TTL 计算，未过期命中不得再打上游；NXDOMAIN 按 NegativeTTL 负缓存，只否定当前 qtype，同名的其他 qtype 必须各自查询，SERVFAIL 按 ServfailTTL 短缓存，两者命中都要返回对应的 ErrNXDomain 与 ErrServfail；单个等待者超过 WaitTimeout 立即返回 ErrTimeout，不能阻塞整个组；客户端配额按令牌桶独立计算，额度耗尽的请求返回 ErrQuotaExceeded，且不得进入单飞组、不得触发上游调用；同一客户端连续请求按传入的 now 补充令牌，令牌上限为 burst，未配置配额的客户端不受限；qps 小于等于零、burst 小于一报 ErrBadQuota，空客户端号报 ErrBadClient；Invalidate 按 qname 删除所有类型的缓存条目，名字比较不区分大小写并忽略末尾的点；Sweep 删除已过期条目并返回条数；缓存条目超过 MaxCacheEntries 时淘汰最久未使用的条目；快照保存未过期缓存条目与配额窗口，恢复后过期条目不能被当成新鲜结果，所有过期判断都以调用方传入的 now 为准；Restore 对损坏数据报 ErrBadSnapshot。

硬约束：单飞组、缓存表与配额表共用互斥锁，组的建立与结果写回必须原子，同一键不能同时存在两个上游调用；等待者全部离开后上游结果仍要写回缓存，且不向已离开的等待者投递；快照与恢复要么整体生效要么完全不生效，恢复出的条目按原 ExpiresAt 判定。

验收：go test ./... 15 条全过，不得改测试、跳过或按固定输入写死；并发与重启恢复自行验证；只做本地实现。
以上现象均在真实生产流量下复现。
## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-a-28`
- 实现文件：`dns_query_deduper.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`QueryDeduper`
