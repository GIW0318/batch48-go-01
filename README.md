# 租户 CPU 配额与突发额度仲裁

## 题面

多租户离线计算集群把 CPU 配额统一交给一个仲裁器之后，投诉集中在三类：小租户的份额被换算成 0，任务永远拿不到时间片；另一个租户在配额被调低后仍保留之前借出去的突发额度，账上出现负数，下一轮又有租户整周期被饿死；总份额已经超卖时，空闲租户的余量还会被同一个活跃租户连续两轮吃干净，其他租户排队等不到配额。调度侧还看到同一租户的节流次数在几分钟内跳了几十倍，而相邻租户一条记录都没有；仲裁器重启后从快照续跑时，上一周期未结算的借用又被当成了新额度。落点 gsb/batch48-go-safety-b-15 的 cpu_burst_guard.go，类型 CpuBurstGuard。接口保持：NewCpuBurstGuard(Options) *CpuBurstGuard、SetShare(tenant string, share int) error、SetQuota(tenant string, periodMs, quotaMs int) error、Throttled(tenant string, at int64) error、Borrow(tenant string, ms int) (int, error)、Usage(tenant string) (Usage, error)、Rebalance() (map[string]int, error)、Snapshot() ([]byte, error)、Restore([]byte) error、Tenants() []string、Idle() int、Borrowed(tenant string) (int, error)。

要修的行为如下。份额为负、超过 10000 报 ErrBadShare；周期非正或配额为负报 ErrBadQuota；租户数到顶报 ErrTenantLimit，新租户首次设置份额或配额时自动建档。份额为零表示该租户不参与本轮分配，但租户本身仍然保留。按份额分配时把周期按权重切给各租户；周期小于最短可表达粒度时配额要向上取整到最小值，不能写成 0 导致该租户完全拿不到 CPU。借用只在空闲余量内批准，返回实际批出的毫秒数；Idle 返回周期内既未分配也未借出的毫秒数，空闲余量按周期扣掉已分配配额与已借出突发；借用请求非正报 ErrBadBorrow；剩余预算为零时再借返回 0 而不是负余额；借用额度按租户单独封顶，不能把全集群的余量一次性借给同一户。某租户借出的突发在其配额被调低后要按比例回收，已运行周期不能凭空出现负余额；配额调低立即生效，未运行完的周期按新配额结算。节流事件按租户累计，Usage 能读出周期、配额、已借出与节流次数，已用毫秒不得为负，节流计数只增不减；Rebalance 按权重重新分配余量并返回每个租户的新配额，前后总分配不得超过周期长度；总份额超卖时，空闲租户的余量不能被同一租户连吃两轮。未知租户的查询报 ErrUnknownTenant。快照要带上每户的周期、配额、借用与节流计数。

硬约束：Borrow 与 Rebalance 会并发进入，同一份空闲余量只能被分配一次，记账不得出现负余额；重启恢复后配额、借用与节流计数必须与中断前一致，恢复过程不得把已借出的额度再借一遍；租户数与单户借用额度都有硬顶。

验收：go test ./... 的 15 条用例全部通过，测试文件不改、不跳过、不按固定输入写死；并发与重启恢复自行构造验证；只做本地实现。
以上现象均在真实生产环境复现。
## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-b-15`
- 实现文件：`cpu_burst_guard.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`CpuBurstGuard`、`Options`、`Usage`
