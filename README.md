# 租户、路由、连接三级令牌桶限速

## 题面

网关的三级限速上线第一周就出了三类事故：某个租户下的一条路由把父级租户的额度一次性借空，同租户其他路由整整一分钟被拒；运维调完系统时间往回拨了几分钟，本来已经耗尽的连接桶又开始放行，超发了十几万条请求；限速服务滚动重启后各层余量全部清零，被打回的请求也没有任何可重试等待时间，客户端只能原地重试把上游打穿。落点 gsb/batch48-go-safety-a-22 的 flow_rate_governor.go，类型 FlowRateGovernor，签名保持：NewFlowRateGovernor() *FlowRateGovernor、SetLimit(level Level, key string, rate float64, burst uint64) error、Allow(tenant, route, conn string, cost uint64, now time.Time) (Decision, error)、Refund(t Ticket) error、Sweep(now time.Time)、Snapshot() GovernorSnapshot、Restore(s GovernorSnapshot) error、Remaining(level Level, key string) uint64。

要修：一次请求必须在租户、路由、连接三层桶上同时扣减，子级可用额度不得超过父级剩余额度，任一层不通过时三层都不许改动；子级余量不足时允许在本桶一个 burst 的范围内借用令牌，借用记入账本，Sweep 到期先用补充出来的令牌还债，未还清的部分直接清零，不允许债务跨窗口累积；单次 cost 大于 burst 的请求要直接拒绝并给出所需的最小 burst，余量不许扣成负数；被拒绝的请求必须返回按缺口和速率算出的可重试等待时长，多个层同时不足时取最长的一个；令牌按调用方传入的时间补充，时钟回拨不得增加额度，也不得把补充起点往回挪，否则随后一次正常调用会凭空多补；Refund 只能退回票据原来所属的层级，退回后不得超过该层当前上限，跨层或重复使用票据要拒绝并单独计数；SetLimit 要校验速率非正、burst 为零、未知层级、空 key 四种输入并分别报错，新桶按满 burst 建立，调低 burst 时余量要钳到新上限；每一层的余量与借用账本都要进快照，恢复时按快照时间戳补桶但不能超过 burst，停机很久之后补桶不得把停机时长无限折算成令牌，超过 burst 的部分必须丢弃；快照版本不符报 ErrBadSnapshot。同一个 key 上的并发 Allow 不能超发，被拒绝的请求也不能把兄弟路由的桶一起改掉。

硬约束：Allow、Refund、SetLimit 与 Sweep 在同一把锁下串行，同一 key 的并发准入以互斥锁加原子余量保证不超发；限速表、余量与借用账本要能快照并在滚动重启后恢复，恢复结果要么整体生效要么完全不生效；租户、路由、连接三层的桶数量按上限封顶，超限一律拒绝而不是部分写入。

验收：go test ./... 15 条全过，不得改测试、跳过或用固定输入写死；并发与重启恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-a-22`
- 实现文件：`flow_rate_governor.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`FlowRateGovernor`
