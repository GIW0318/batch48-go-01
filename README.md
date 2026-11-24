# batch48-go-safety-d-03

## 题面

安全运营中心的值班表上，审计事件在凌晨两点断了两个小时：网关侧显示提交成功，检索端一条都查不到；排查发现通道队列满之后事件被静默丢弃，缺口区间既没有记录也没有告警。另一个通道在维护窗口进入 drain，新事件没有报错，而是落进一个没人消费的死队列，第二天才被发现。字段脱敏也不一致，同一批事件里 token 与 password 被替换成空串，工单号却被连带涂掉，安全同学拿到投影副本根本没法定位。更离谱的是服务重启后通道序号从 1 重新开始，检索端按序号去重把新事件全吞了，缺口区间也对不上。落点 gsb/batch48-go-safety-d-03 的 audit_event_router.go，类型 AuditRouter，按租户与通道投影脱敏副本后投递，并维护每通道有界队列、序号连续性与严重级升格规则。

接口保持：NewAuditRouter(redactFields []string) *AuditRouter、OpenChannel(name string, capacity int) error、CloseChannel(name string) error、Submit(evt RawEvent) (SubmitResult, error)、Project(evt RawEvent) (ProjectedEvent, error)、BeginDrain(channel string) error、DrainChannel(channel string) ([]ProjectedEvent, error)、Pending(channel string) (int, error)、Gaps(channel string) ([]GapRange, error)、SetSeverityFloor(channel string, sev Severity) error、Snapshot() (RouterSnapshot, error)、Restore(snap RouterSnapshot) error、Stats() RouterStats、Channels() []string。RawEvent 带 EventID、Tenant、Channel、Severity、Fields；ProjectedEvent 带 Sequence 与脱敏后的 Fields；SubmitResult 带 Accepted、Duplicate、Dropped、Sequence；GapRange 带 From 与 To；Severity 从 Debug 到 Critical 五档，默认脱敏键包含 password、token、secret、authorization、cookie，配置里的键名一并生效；Project 不要求通道存在，通道存在时套用其严重级下限。

要修的行为：（1）EventID 为空、租户为空也照收，重复 EventID 不去重，同一事件入队两次。（2）未知通道返回成功，事件被丢进一个隐式通道，检索端永远看不到。（3）队列满时直接覆盖最旧事件，既不计丢弃数也不形成缺口区间。（4）序号按时间戳生成，同一毫秒内事件序号重复，不同通道共用一个计数器。（5）脱敏只处理默认键，配置里追加的字段原样透出，键名大小写不同就漏。（6）投影直接改写原始事件字段，调用方手里的原始事件被污染。（7）严重级低于通道下限时原样投递，升格规则不生效；越界严重级也照收。（8）通道进入 drain 后新事件落进内部死队列，调用方拿不到拒绝信号。（9）相邻丢弃没有合并成一段区间，中间成功路由的事件也没有把区间切开。（10）快照把事件正文一起存下来，恢复后重复投递已经投递过的事件。（11）恢复后通道序号回到 1，累计计数清零。（12）并发提交时同一通道的序号重复，入队计数与实际队列长度对不上。

硬约束：同一通道的并发提交必须串行分配序号，EventID 去重与入队计数要原子完成，重放同一批输入不得产生重复投递；快照只保存队列元数据与计数，不含事件正文，恢复后序号继续递增而不是从头开始；通道容量与脱敏字段表都有硬上限，超限返回可识别错误而不是静默扩张。

验收：go test ./... 15 条全过，不得修改测试、不得跳过、不得按固定输入写死；并发提交与重启恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块路径与包名

- module: gsb/batch48-go-safety-d-03
- 根目录包名: task（audit_event_router.go）
- 测试目录: tests（package task）
- Go 1.23，仅标准库
