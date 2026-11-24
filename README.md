# batch48-go-safety-d-08

- 模块路径：`gsb/batch48-go-safety-d-08`
- 包名：`task`
- 实现文件：`dns_update_tsig_guard.go`
- 测试目录：`tests/`

## 运行方式

```bash
go test ./...
```

## 题面

值班同事凌晨被 DNS 告警叫醒：某个区的 A 记录被改到了境外地址，查动态更新日志只有一条“更新成功”，既看不出用的哪把密钥，也没有记录来源请求号；同一个更新请求因为重传被应用了两次，SOA 序列一次跳了两号，从库直接判定主从失序。还有一处更隐蔽：一把已经下线的密钥在吊销后仍然能通过校验，区域里的通配记录被当成了目标名已经存在的证据；告警平台上这几把密钥的失败计数长期为零，值班无法判断是偶发还是被人扫，运维只能把整个区域改成只读。请修 gsb/batch48-go-safety-d-08 模块里的 dns_update_tsig_guard.go，类型 TsigAuthzGuard，包名 task，只用标准库。

接口：NewTsigAuthzGuard(TsigOptions) *TsigAuthzGuard；BindKey(TsigKeySpec) error；RevokeKey(name string) error；RotateKey(name, algorithm string) (TsigKeySpec, error)；SetZoneGrant(ZoneGrant) error；Verify(DnsUpdate, now int64) (VerifiedUpdate, error)；ApplyUpdate(VerifiedUpdate) (int64, error)；Serial(zone string) int64；Pending() int；KeyFailures(name string) int；Snapshot() ([]byte, error)；Restore([]byte) error；Stats() TsigStats。TsigKeySpec 含 Name、Algorithm、DigestTag、Generation；ZoneGrant 含 Zone、Keys、Types；DnsUpdate 含 RequestID、KeyName、Algorithm、Generation、Zone、Name、Type、At、DigestTag、ExpectExists；VerifiedUpdate 含 RequestID、Zone、Name、Type、DigestTag；TsigOptions 含 WindowSeconds、MaxPending、MaxKeys；TsigStats 含 Keys、Verified、Applied、Rejected、Pending、Failures。

需要修的行为：

一、BindKey 的名字、算法或摘要标签为空按 ErrKeyConfig 拒绝，算法只接受 hmac-sha256 与 hmac-sha512；重复名字按 ErrKeyExists 拒绝；超过 MaxKeys 按 ErrKeyCapacity 拒绝。

二、SetZoneGrant 的区域为空、密钥列表为空或记录类型为空按 ErrGrantConfig 拒绝；同一把密钥在同一区域只保留一份授权。

三、Verify 对未知密钥返回 ErrKeyUnknown，对算法不符返回 ErrAlgorithmMismatch，对已吊销密钥返回 ErrKeyRevoked。

四、RotateKey 对未知密钥返回 ErrKeyUnknown，成功后代数加一并返回新规格，绑定后的初始代数为一；旧代数的请求一律按 ErrGenerationStale 拒绝。

五、摘要标签与密钥不符按 ErrDigestMismatch 拒绝，同时该密钥的失败计数加一，Stats 的 Rejected 同步累加。

六、区域未授权给该密钥返回 ErrZoneNotGranted；记录类型不在授权列表返回 ErrTypeNotGranted；更新名字不属于该区域返回 ErrNameNotOwned。

七、ExpectExists 为真时目标名不存在返回 ErrConditionUnmet，存在才放行；判定按精确名进行。

八、同一 RequestID 在未结算窗口内重复出现返回 ErrRequestReplay，Pending 只统计尚未应用的请求。

九、ApplyUpdate 只接受本实例签发且尚未应用的请求，重复应用返回 ErrRequestReplay；应用成功后该区域的 SOA 序列从零开始逐次加一并返回。

十、去重、前置条件与序列递增必须在同一事务内完成，并发应用同一请求只能成功一次；吊销即时生效，吊销后已签发的请求也不得应用。

十一、时钟偏移超出窗口但摘要正确时同样拒绝，并把这次拒绝计入密钥失败计数以驱动告警。

十二、通配符名的更新前置条件按精确名判定，存在通配记录不等于目标名已存在。

硬约束：快照包含密钥与代数、区域授权、各区域序列、未结算请求与失败计数；恢复后去重窗口与序列水位连续，重复请求仍被拒绝；坏快照按 ErrTsigSnapshot 拒绝且不破坏内存状态。验收：go test ./... 的 15 条用例全过；不改测试、不跳过、不按固定输入写死；并发与重启自行验证；只做本地实现。
