# 上游并发自适应限流、熔断半开探测与重试共用窗口

## 题面

上游调用治理上线后，网关在晚高峰出现过三次典型故障：并发窗口从 32 收缩到 8 之后，队列里剩下的两百多个请求仍然一个个排队，等它们全部拿到旧额度时上游已经被压垮；熔断进入半开探测，一个探测成功之后窗口停在 1，后续流量继续超时，恢复时间被拉长到十分钟；上游其实已经恢复，但错误率还高，半开状态几乎每个请求都被当成探测放行，等于没有熔断。回放还发现：窗口收缩时，已经排在队列里的请求没有任何人被拒绝，新窗口形同虚设；熔断重新打开后，冷却时间从零重新算，刚重启的实例立刻又开始放量；业务错误和慢请求混在一起统计，一次参数校验失败也被当成上游超时，窗口被连续误收缩。落点 gsb/batch48-go-safety-a-29 的 adaptive_concurrency_governor.go，类型 ConcurrencyGovernor，签名保持：NewConcurrencyGovernor(opts GovernorOptions) *ConcurrencyGovernor、Acquire(ctx context.Context) (Ticket, error)、Release(t Ticket, latency time.Duration, ok bool) error、State() State、Window() int、Inflight() int、Queued() int、SetBounds(min, max int) error、Stats() GovernorStats、Snapshot() ([]byte, error)、Restore(data []byte) error。

要修：窗口初始值取自 InitialWindow，在途数达到窗口时 Acquire 进入等待队列，队列满报 ErrQueueFull；等待中 context 取消要立即返回取消错误，不能继续占位；Release 未知票据报 ErrBadTicket，重复释放同一票据同样报错；成功样本累计到 SampleWindow 后窗口按 GrowthStep 加性增长，超过 MaxWindow 截断，统计完一批样本后计数清零；延迟达到 Timeout 记为超时，超时样本累计到 SampleWindow 后窗口按 ShrinkFactor 乘性收缩并向下取整，低于 MinWindow 截断；业务错误只计入错误率，不改变窗口；错误率超过 ErrorRateThreshold 时熔断打开，Acquire 在冷却期内快速返回 ErrCircuitOpen；冷却结束进入半开，只放行一个探测请求，其余请求继续快速失败；错误率仍高于阈值时，半开探测按 ProbeBackoff 间隔放行，不能连续放行全部请求；探测成功关闭熔断并把窗口恢复到收缩前水平之上，探测失败重新打开熔断并重新计时；SetBounds 对 min 小于一、max 小于 min 报 ErrBadBounds，新边界立即作用于后续 Acquire 并把当前窗口钳进边界；重试请求与正常请求共用同一窗口，不得额外开额度；Stats 报告窗口、在途、排队、样本数与错误率；快照保存窗口、熔断状态、打开时刻与采样窗口，恢复后熔断冷却按原打开时刻继续计算，不能重置；Restore 对损坏数据报 ErrBadSnapshot；窗口调整与在途计数对并发 Acquire 必须原子可见，收缩后多出来的等待者要快速失败而不是继续排队。

硬约束：Acquire 的准入、等待队列、窗口与熔断状态在同一把锁下推进，容量判定与在途计数必须原子，不能出现瞬时超发；窗口、熔断状态与采样窗口要能快照并在重启后恢复，恢复要么整体生效要么完全不生效；错误率只按样本窗口统计，业务错误不得触发窗口收缩。

验收：go test ./... 15 条全过，不得改测试、跳过或按固定输入写死；并发与重启恢复自行验证；只做本地实现。
以上现象均在真实生产流量下复现。
## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-a-29`
- 实现文件：`adaptive_concurrency_governor.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`ConcurrencyGovernor`
