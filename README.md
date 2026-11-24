# batch48-go-safety-d-09

- 模块路径：`gsb/batch48-go-safety-d-09`
- 包名：`task`
- 实现文件：`snmpv3_usm_guard.go`
- 测试目录：`tests/`

## 运行方式

```bash
go test ./...
```

## 题面

网管平台升级 SNMPv3 之后，几台核心交换机不再上报任何 trap，只有手动重启采集器才恢复一阵；另一批设备报认证失败，抓包发现报文里的引擎 ID 与本地保存的不一致，报文时间还停在几天前。安全审计还指出，trap 里的某些 OID 本不该发给外部接收端，视图过滤形同虚设，而且引擎的 boots 计数已经跑到很高，再自增就会回绕，平台没有任何刹车；同一个用户在两个采集器上的本地化结果也不一样，换一台设备就得重新配一遍口令。请修 gsb/batch48-go-safety-d-09 模块里的 snmpv3_usm_guard.go，类型 UsmGuard，包名 task，只用标准库。

接口：NewUsmGuard(UsmOptions) *UsmGuard；AddUser(UsmUser) error；RemoveUser(name string) error；Localize(user string) (UsmKeys, error)；RotateUserKeys(user string) (int, error)；VerifyMessage(UsmMessage) error；SetView(name string, entries []ViewEntry) error；AddTrapTarget(TrapTarget) error；ProjectTrap(target string, varbinds []string) (TrapNotification, error)；ResetEngineBoots(operator string) error；AdvanceEngineTime(delta int) int；EngineBoots() int；Snapshot() ([]byte, error)；Restore([]byte) error；Stats() UsmStats。UsmUser 含 Name、AuthProto、PrivProto、Level、AuthPass、PrivPass；UsmKeys 含 User、EngineID、AuthKey、PrivKey、Generation；UsmMessage 含 User、EngineID、Boots、EngineTime、AuthOK、PrivOK、Generation、RequestID；ViewEntry 含 OID、Included；TrapTarget 含 Name、Address、View、User；TrapNotification 含 Target、Varbinds、Dropped、Sent；UsmOptions 含 EngineID、TimeWindow、MaxBoots、MaxUsers、MaxTargets；UsmStats 含 Users、Verified、Rejected、Traps、Filtered。

需要修的行为：

一、AddUser 的名字为空、认证协议不受支持、Level 不在三种标准级别内、需要认证却没有口令、需要加密却没有私密口令，一律按 ErrUserConfig 拒绝；重名按 ErrUserExists 拒绝，超过 MaxUsers 按 ErrUserLimit 拒绝。

二、Localize 对未知用户返回 ErrUserUnknown；本地化密钥带上引擎 ID 和当前代数，代数初始为一，同一用户两次调用结果一致，不同用户互不相同，authPriv 用户的两把密钥都不能为空。

三、VerifyMessage 对未知用户返回 ErrUserUnknown，引擎 ID 不一致返回 ErrEngineMismatch。

四、报文 boots 小于本地引擎 boots 按 ErrTimeWindow 拒绝；engineTime 落在本地时间窗之外同样按 ErrTimeWindow 拒绝。

五、认证级别要求认证而报文 AuthOK 为假时返回 ErrAuthProof；authPriv 级别 PrivOK 为假时返回 ErrPrivProof。

六、RotateUserKeys 对未知用户返回 ErrUserUnknown，成功后代数加一并返回新代数；旧代数的报文按 ErrKeyGeneration 拒绝。

七、RemoveUser 未知用户返回 ErrUserUnknown，删除后该用户的报文与本地化调用都按未知用户拒绝。

八、SetView 的名字为空、条目为空或 OID 为空按 ErrViewConfig 拒绝；AddTrapTarget 引用未知视图返回 ErrViewUnknown，引用未知用户返回 ErrUserUnknown，重复目标名按 ErrTargetConfig 拒绝，超过 MaxTargets 按 ErrTargetLimit 拒绝。

九、ProjectTrap 按视图做最长前缀过滤，被排除的 varbind 丢弃并计入 Dropped，发出的通知带 Sent 标记，Stats 的 Traps 与 Filtered 同步累加；未知目标返回 ErrTargetUnknown。

十、AdvanceEngineTime 推进本地引擎时间并返回新值，EngineBoots 返回当前 boots，初始为一；ResetEngineBoots 在操作人为空时按 ErrBootsResetDenied 拒绝。

十一、boots 计数接近上限时必须进入只读模式并要求管理员显式重置，不能继续自增到回绕。

十二、视图过滤后 trap 载荷为空时仍要发出带空 varbind 的通知并记账，不能静默丢弃整条通知。

硬约束：并发校验同一用户时只能看到同一份密钥代际与封锁状态，不出现半切换；快照包含用户、密钥代数、引擎 boots 与时间、视图、目标与统计；恢复后上述状态逐项一致，坏快照按 ErrUsmSnapshot 拒绝且不破坏内存状态。验收：go test ./... 的 15 条用例全过；不改测试、不跳过、不按固定输入写死；并发与重启自行验证；只做本地实现。
