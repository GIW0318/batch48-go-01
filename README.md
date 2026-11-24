# batch48-go-safety-d-10

- 模块路径：`gsb/batch48-go-safety-d-10`
- 包名：`task`
- 实现文件：`hostkey_pin_guard.go`
- 测试目录：`tests/`

## 运行方式

```bash
go test ./...
```

## 题面

运维做主机密钥巡检时发现，跳板机到一批生产主机的连接在密钥轮换当天集体失败，重试多少次都连不上，只能删掉本地记录手工重建；另一个租户报障说同一台主机名解析出两个地址，其中一个地址的指纹对不上，客户端却整体放行，事后查审计只写着“已信任”，看不出是哪个地址出的问题。还有一处：一台主机被吊销后，下游网关过了半小时还在拿旧指纹放行。请修 gsb/batch48-go-safety-d-10 模块里的 hostkey_pin_guard.go，类型 HostKeyPinGuard，包名 task，只用标准库。

接口：NewHostKeyPinGuard(PinOptions) *HostKeyPinGuard；Pin(PinRequest) (PinDecision, error)；Verify(VerifyRequest) (PinDecision, error)；BeginRotation(tenant, host string, port int, newFingerprint string, at int64) error；CompleteRotation(tenant, host string, port int, at int64) error；Revoke(tenant, host string, port int) error；Propagate(hops int) (int, error)；Entries(tenant string) []HostKeyEntry；Lookup(tenant, host string, port int) (HostKeyEntry, bool)；AuditLog() []PinAudit；Snapshot() ([]byte, error)；Restore([]byte) error；Stats() PinStats。PinRequest 含 Tenant、Hostname、Port、Addresses、Fingerprints、Trust、At；VerifyRequest 含 Tenant、Hostname、Port、Presented、At；PinDecision 含 Allowed、Reason、Fingerprint、NewHost；HostKeyEntry 含 Tenant、Hostname、Port、Address、Fingerprint、PinnedAt、Status；PinAudit 含 Action、Hostname、Port、Address、Reason；PinOptions 含 MaxEntriesPerTenant、OverlapSeconds、MaxHops；PinStats 含 Pinned、Verified、Denied、Revoked、Propagated、Rotations。

需要修的行为：

一、Pin 的租户、主机名为空，端口不为正，地址列表为空，或指纹数量与地址数量不一致，按 ErrPinInput 拒绝；未显式声明信任按 ErrTrustRequired 拒绝，首次信任必须显式写入。

二、首次信任成功后按地址逐条落钉扎记录，Reason 固定为 trusted_first_use，NewHost 为真，Pinned 计数按条目累加。

三、已知主机已存在同端口同地址的钉扎记录，重复 Pin 按 ErrPinnedAlready 拒绝；单租户条目数超过 MaxEntriesPerTenant 按 ErrEntryLimit 拒绝，且一条都不写入。

四、Verify 对没有钉扎记录的主机返回 ErrHostUnknown；指纹与钉扎一致时放行且 Reason 为 pinned_match。

五、指纹不一致按 ErrFingerprintMismatch 拒绝，不得静默改钉新指纹。

六、BeginRotation 对未知主机返回 ErrHostUnknown，新指纹为空按 ErrPinInput 拒绝，已在轮换中的主机再次开启按 ErrRotationActive 拒绝。

七、轮换重叠期内新旧指纹都可放行，Reason 为 rotation_overlap；CompleteRotation 之后旧指纹按 ErrFingerprintMismatch 拒绝、新指纹按 pinned_match 放行；没有进行中的轮换按 ErrNoRotation 拒绝。

八、Revoke 删除该主机的钉扎记录并留下吊销标记，之后 Verify 按 ErrHostRevoked 拒绝；吊销未知主机按 ErrHostUnknown 拒绝；重新显式信任可以恢复。

九、Propagate 的跳数小于一或超过 MaxHops 按 ErrHopBudget 拒绝，合法跳数返回本次推送的条目数，吊销标记也计入，Propagated 同步累加。

十、Entries 按主机名、端口、地址升序返回；AuditLog 按发生顺序记录 pin、verify、rotate、revoke 动作与决策原因，拒绝也要留痕。

十一、重叠窗到期时若尚未完成一次成功匹配，新旧指纹同时失效会让该主机彻底不可连，收敛动作必须先于窗口关闭。

十二、同一主机名解析到多个地址时按地址分别钉扎，任一新地址指纹不匹配即整体拒绝并指出具体地址。

硬约束：同一主机的并发首次信任只能写入一套钉扎记录，计数不重复；轮换、吊销与校验并发时只能看到切换前的完整状态或切换后的完整状态。快照包含钉扎记录、轮换状态、吊销标记与统计，恢复后 Entries、Lookup 与 Verify 行为一致，坏快照按 ErrPinSnapshot 拒绝且不破坏内存状态。验收：go test ./... 的 15 条用例全过；不改测试、不跳过、不按固定输入写死；并发与重启自行验证；只做本地实现。
