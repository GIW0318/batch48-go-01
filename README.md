# SRV 服务发现的选择器：权重轮询、健康抑制与 TTL 兜底

## 题面


服务发现是防流量劫持的入口；攻击者污染 SRV 集合或粘性 key，就能把调用方引到受控后端。服务发现切换机房的那个夜里，调用方出现大面积超时：SRV 记录里有一个机房整体不健康，权重轮询仍然把请求均匀打过去；权重为 0 的备份实例被当成正常后端反复选中；集合更新之后，粘性 key 还指着已经下线的 target，调用方持续收到连接拒绝；TTL 到期而新集合迟迟未到时，选择器直接报错，把整条链路带崩。

落点 gsb/batch48-go-safety-a-20 的 srv_discovery_balancer.go，类型 SRVBalancer，构造函数 NewSRVBalancer(Policy) (*SRVBalancer, error)，Policy 携带 FailThreshold、Cooldown 与 MaxKeys，记录类型为 SRVRecord（Host、Port、Priority、Weight、TTL），目标类型为 Target（Host、Port）。对外签名保持：Update(records []SRVRecord) error、Pick(key string) (Target, error)、ReportHealth(target Target, ok bool) error、Expire(now time.Time)、SetPolicy(p Policy) error、Targets() []Target、Healthy(target Target) bool、Stats() BalancerStats、Snapshot() ([]byte, error)、Restore([]byte) error。

要修的地方：空集合报 ErrEmptyRecordSet，主机为空、端口为零或 TTL 非正报 ErrInvalidRecord，同一 host:port 重复出现报 ErrDuplicateTarget；选择只在最低优先级组内进行，正权重目标之间按权重平滑轮询，权重为 0 的记录不参与常规轮询，只在该组没有正权重健康目标时兜底并按顺序轮转；连续失败达到 FailThreshold 才进入抑制，任意一次成功要清零连续失败计数；抑制期结束后只放行一个试探请求，试探未出结果前该目标不得再次被选中，试探成功恢复、失败重新抑制；所有目标都被抑制时 Pick 必须报 ErrNoHealthyBackend，不得回退到权重为 0 的记录；记录 TTL 到期而新集合未到时继续用旧集合，并把 Stats 里的 Stale 置为真，新集合到达后清除；同一 key 在集合未变时保持粘性，重复 Pick 返回同一个 target，不同 key 继续推进轮询；快照要保存权重累计器、抑制计数、冷却截止、试探标记、零权重游标与粘性表，恢复后轮询序列必须接着走而不是从头开始；损坏快照报 ErrBadSnapshot 且不改动现有状态；未知 target 报 ErrUnknownTarget，非法策略报 ErrInvalidPolicy，尚未加载集合时 Pick 报 ErrNoRecords。集合更新后，粘性 key 指向的 target 若已不存在，要立即重选并记录迁移次数，不能继续返回旧 target。新旧集合端口相同但优先级不同时，要按优先级重排并重置该优先级的轮询位置，不能把两组权重直接相加。所有后端都被抑制时必须返回显式的无健康后端错误，权重为 0 的记录不能成为绕过抑制的退路。

硬约束：Update、Pick、ReportHealth、Expire、SetPolicy、Targets、Healthy、Stats 与快照共用一把互斥锁，权重累计器、抑制计数与试探标记同批提交，读方不得看到半更新状态；权重累计器与零权重游标必须可持久化，重启后同一集合的轮询序列不能回退；TTL 到期只标记陈旧，不得清空仍在服务的旧集合。

验收：go test ./... 15 条全过，测试不得修改、跳过或按固定输入写死；并发与重启自行验证；只做本地实现。以上现象均在真实生产流量下复现。
## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-a-20`
- 实现文件：`srv_discovery_balancer.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`SRVBalancer`、`SRVRecord`、`Target`、`Policy`、`BalancerStats`
