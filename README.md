# batch48-go-safety-d-06

- 模块路径：`gsb/batch48-go-safety-d-06`
- 包名：`task`
- 实现文件：`nac_port_binding_guard.go`
- 测试目录：`tests/`

## 运行方式

```bash
go test ./...
```

## 题面

园区无线控制器上线 802.1X 之后，同一台笔记本换到隔壁会议室就上不了网，运维在接入交换机上还能看到它挂在原来的端口上，同一台设备在两栋楼之间来回插拔时，绑定有时跟着走、有时留在旧端口占着名额；另一栋楼报障说隔离区里堆了十几个端口，新来的访客认证直接失败，可看板上隔离端口一个都没释放。审计抽查还发现，端口被运维关闭过一次之后，重新启用时设备没有重新认证就恢复了网络，租期也算少了将近一半。请修 gsb/batch48-go-safety-d-06 模块里的 nac_port_binding_guard.go，类型 NacBindingGuard，包名 task，只用标准库。

接口：NewNacBindingGuard(NacOptions) *NacBindingGuard；Authenticate(port string, dev DeviceIdentity, vlan int) (NacBinding, error)；Rebind(port string, dev DeviceIdentity, vlan int) (NacBinding, error)；Release(port string) error；Isolate(port string, reason string) error；SetVlanWhitelist(vlans []int) error；SuspendPort(port string) error；ResumePort(port string) (bool, error)；Expire(elapsed int) []string；BindingOf(port string) (NacBinding, bool)；IsolatedPorts() []string；AuditTrail() []NacAuditEvent；Snapshot() ([]byte, error)；Restore([]byte) error；Stats() NacStats。DeviceIdentity 含 MAC、Tenant、CertCN；NacBinding 含 Port、MAC、Tenant、Vlan、Lease、Remaining、State、Reason；NacOptions 含 LeaseSeconds、IsolateCapacity、MaxBindings、AllowedVlans；NacStats 含 Authenticated、Rebound、Released、Isolated、Expired、Denied、Active、AuditEvents。

需要修的行为：

一、端口号或设备 MAC 为空按 ErrInvalidPort 拒绝，不写入任何绑定，同时 Denied 计数加一。

二、VLAN 不在白名单按 ErrUnauthorizedVlan 拒绝并计入 Denied；白名单为空表示不限制；SetVlanWhitelist 整表替换旧名单。

三、同一设备在同一端口重复认证视为续租，Remaining 重置为 Lease，Active 不增加；端口已被其他设备占用按 ErrPortBound 拒绝且原绑定保持不变。

四、设备已经绑在别的端口时，直接认证新端口按 ErrIdentityConflict 拒绝，必须走 Rebind；Rebind 成功后旧端口释放、新端口建立绑定并重置租期，Rebound 计数加一。

五、Rebind 的设备从未认证过按 ErrUnknownDevice 拒绝；Release 未知端口按 ErrUnknownPort 拒绝。

六、Expire 按传入的经过秒数扣减全部绑定的剩余租期，归零的绑定被释放，隔离端口一并释放隔离位；返回被释放的端口并按字典序升序，Expired 与 Released 各自计数。

七、Isolate 把已绑定端口转入隔离域并记录原因，State 变为 isolated；未知端口按 ErrUnknownPort 拒绝，失败不得写入隔离条目。

八、IsolatedPorts 按升序返回；绑定总数受 MaxBindings 硬顶，超限按 ErrBindingLimit 拒绝，隔离端口数量另受 IsolateCapacity 硬顶。

九、重复端口或设备不会重复计数；Stats 的 Active 只反映当前仍在表中的绑定数；快照包含绑定、隔离集合、租期与统计，恢复后 BindingOf 的 Remaining、IsolatedPorts 与 Stats 与快照时一致，坏快照按 ErrBadBindingSnapshot 拒绝且不破坏内存状态。

十、审计按状态迁移顺序记账，动作固定为 authenticate、rebind、release、isolate、expire，逐条记录端口与设备，AuditEvents 与条目数一致。

十一、端口被管理性关闭后绑定进入挂起而非解除，重新启用时必须重新认证，挂起期不计入租期消耗。

十二、隔离域已满时新漂移设备直接拒绝认证，不能挤占已有隔离端口，已有隔离端口一条都不能被顶掉。

硬约束：Authenticate、Rebind、Isolate 与 Stats 并发时只看到完整状态，不出现半个绑定；同一设备的并发首次认证只能产生一条绑定，计数不重复。验收：go test ./... 的 15 条用例全过；不改测试、不跳过、不按固定输入写死；并发与重启自行验证；只做本地实现。
