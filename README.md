# batch48-go-safety-c-07

- 模块路径：`gsb/batch48-go-safety-c-07`
- 包名：`task`
- 实现文件：`scan_lease_scheduler.go`
- 测试目录：`tests/`

## 题面

资产扫描平台最近出现两类事故：大促前扩容到二十个执行器，结果同一批主机被两个执行器同时扫，重复告警翻倍；还有几台执行器在心跳超时之后仍然活着，它们迟到的扫描结果把新执行器已经写好的结果覆盖成了过期数据。调度器重启之后问题更明显：一部分在途分片既没被回收也没被重派，任务卡在“进行中”一直不动，值班同学只能手工补单。

请修 gsb/batch48-go-safety-c-07 模块里的 scan_lease_scheduler.go，类型名为 ScanLeaseScheduler，包名 task，只用标准库。接口签名保持现状：NewScanLeaseScheduler(SchedulerOptions) *ScanLeaseScheduler、Enqueue(Target) error、Lease(string) (*Shard, error)、Renew(uint64) error、Complete(uint64, ShardResult) error、Reclaim(time.Time) int、Reported(string, int) (ShardResult, bool)、Pending() int、InFlight() int、Results() []ShardResult、Stats() SchedulerStats、Snapshot() ([]byte, error)、Restore([]byte) error。Target 含 ID、Host、Units、Shards、Priority；Shard 含 TargetID、Index、Begin、End、Seq、LeaseID、Worker、Expires；ShardResult 含 TargetID、Index、Seq、Findings、Digest；SchedulerOptions 含 LeaseTTL、MaxShardsPerTarget、MaxPending、Now；SchedulerStats 含 Pending、Leased、Completed、Reclaimed、Enqueued。

需要修的行为：

一、Enqueue 校验：ID 或 Host 为空、Units 小于 1、Shards 小于 1、Shards 超过 MaxShardsPerTarget 返回 ErrInvalidTarget，且不改变任何计数。

二、分片边界：Index 从 0 起，Begin 等于 Index 乘 Units 除以 Shards，End 等于 Index 加一乘 Units 除以 Shards，相邻分片首尾相接，最后一片的 End 等于 Units。

三、同一 ID 重复 Enqueue 且 Shards 不变或变小返回 ErrDuplicateTarget；Shards 变大按扩容处理，只重切未派发与未完成的分片，在途分片保留原边界与序号。

四、派发顺序：Priority 数值小的先派，同优先级按目标 ID 升序，再按分片 Index 升序。

五、Lease 的租约号从 1 单调递增；每次派发把该分片的 Seq 加一，首次为 1，并写入 Worker 与到期时刻，到期时刻等于当前时间加一个 LeaseTTL；没有待派分片返回 ErrNoWork。

六、同一分片同时最多一个有效租约；待派分片总数超过 MaxPending 时 Enqueue 返回 ErrTargetLimit。

七、Renew 只作用于在途租约，未知租约号返回 ErrUnknownLease，到期时刻不晚于当前时间的返回 ErrLeaseExpired，成功则把到期时刻顺延一个 LeaseTTL。

八、Complete 要求结果的 TargetID、Index 与租约匹配，否则返回 ErrInvalidResult 且租约保持有效；租约已到期返回 ErrLeaseExpired 且不落库。

九、结果按 TargetID 与 Index 去重，先落库的生效；分片重派后旧执行器迟到的结果不得覆盖，只能被拒绝；Results 按 TargetID、Index 升序返回。

十、Reclaim 把到期时刻不晚于入参时间的租约全部回收，返回回收条数，分片回到待派队列并保留 Seq，重派时再加一；已完成的分片永远不再派发。

十一、Stats 返回待派、在途、已完成、累计回收与累计入队的分片数；Pending、InFlight 与 Results 长度必须与统计一致。

十二、Snapshot 覆盖待派队列顺序、在途租约的租约号与序号及到期时刻、结果表与统计，Restore 到同配置实例后逐项一致，坏字节返回 ErrBadSnapshot。

硬约束：派发与回收由同一把锁串行，并发 Lease 不得超发，也不得把同一分片同时交给两个执行器；重启后待派与在途必须逐条一致，不允许把在途分片重复入队；LeaseTTL 为零或负按 30 秒、MaxShardsPerTarget 按 64、MaxPending 按 4096，任何一次失败的 Enqueue 不得留下半个目标。

验收标准：go test ./... 的 15 条用例必须全部通过；不得修改测试、跳过用例或按固定输入写死；并发与快照恢复场景自行验证；只做本地实现，不引入第三方库。

## 运行

```bash
go test ./...
```
