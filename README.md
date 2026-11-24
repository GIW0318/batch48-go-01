# batch48-go-safety-d-07

- 模块路径：`gsb/batch48-go-safety-d-07`
- 包名：`task`
- 实现文件：`flow_mirror_audit_guard.go`
- 测试目录：`tests/`

## 运行方式

```bash
go test ./...
```

## 题面

安全运营中心上线全流量镜像后，审计同事在镜像包里看到了完整的会话载荷和未脱敏的内网地址，合规直接打回；同一时刻镜像目标口的包速率被一行突发流量打满，采集进程开始丢包，主转发面也跟着抖动。更麻烦的是，同一条流在两台采集器上的采样结果对不上，回放时一半连接只有单向报文；为了压住噪声，运维把采样率从一比一调到四比一，结果两台采集器上同一条连接的命中序号完全不同，回放对不齐，值班只能把镜像会话整个关掉。请修 gsb/batch48-go-safety-d-07 模块里的 flow_mirror_audit_guard.go，类型 MirrorAuditGuard，包名 task，只用标准库。

接口：NewMirrorAuditGuard() *MirrorAuditGuard；AddPolicy(MirrorPolicy) error；DeletePolicy(id string) error；AddSession(MirrorSession) error；Match(FlowKey) (MirrorPolicy, bool)；Sample(FlowKey, MirrorPolicy, seq int64) bool；Project(FlowKey, MirrorPolicy, map[string]string) map[string]string；SetTarget(MirrorTarget) error；Distribute(sessionID string, packets int) (int, error)；SessionStats(id string) (MirrorSession, bool)；TargetUsed(name string) (int, bool)；Snapshot() ([]byte, error)；Restore([]byte) error；Stats() MirrorStats。FlowKey 含 SrcIP、DstIP、SrcPort、DstPort、Proto、Tenant；MirrorPolicy 含 ID、Tenant、Priority、SampleRate、Granularity、Fields、Family、HighPriority；MirrorSession 含 ID、PolicyID、Target、Tenant、Packets、Mirrored；MirrorTarget 含 Name、MaxPPS、Used、Healthy；MirrorStats 含 Policies、Sessions、Matched、Sampled、Projected、Delivered、Dropped、TargetsDown。

需要修的行为：

一、策略 ID 为空、SampleRate 小于一、Priority 为负、Family 不是 ipv4 或 ipv6，一律按 ErrPolicyConfig 拒绝；重复 ID 按 ErrPolicyExists 拒绝。

二、Match 只命中租户一致且字段合法的流；租户对不上或关键字段为空时返回 false，不得退化成全通配。

三、Sample 必须确定：同一五元组同一序号永远同一结果；SampleRate 为一时全部命中；SampleRate 为 N 时连续 N 个序号里恰好命中一个，不随调用顺序漂移。

四、Project 只保留策略 Fields 白名单里的头部字段，其余字段丢弃；IP 字段按 Granularity 前缀掩码后再返回，掩码不得改动调用方传入的 map。

五、SetTarget 的名字为空或 MaxPPS 不为正按 ErrTargetConfig 拒绝；Healthy 为 false 的目标不再接收新报文。

六、AddSession 的策略不存在按 ErrPolicyUnknown 拒绝，目标未登记按 ErrTargetUnknown 拒绝，重复会话 ID 按 ErrSessionExists 拒绝。

七、Distribute 按目标剩余配额投递，返回实际投递数，会话的 Mirrored 累计实际投递数，超出的部分计入 Dropped，不得阻塞调用方；配额用尽后继续投递返回零。

八、目标不可用时降级为只镜像 HighPriority 策略，低优先级会话投递数为零且照样记账；Match、Sample、Project 各自累计 Matched、Sampled、Projected，Distribute 累计 Delivered 与 Dropped，TargetsDown 反映不可用目标数。

九、DeletePolicy 删除未知策略按 ErrPolicyUnknown 拒绝，删除成功后 Policies 减一。

十、IPv6 掩码粒度非法时整条策略必须拒绝，不能回退到默认粒度继续镜像。

十一、同一流命中多条策略时取优先级最高的一条，采样率不叠加，删除最高优先级策略后重新按次高策略裁决。

十二、策略删除后其已建立的采样状态保留到当前窗口结束，删除瞬间不得让该流采样率突变。

硬约束：策略、目标与会话的增删和投递并发时只看到完整表；目标配额是硬上限，任何路径都不得超发；快照恢复后策略、目标用量与会话计数一致，坏快照按 ErrMirrorSnapshot 拒绝且不破坏内存状态。验收：go test ./... 的 15 条用例全过；不改测试、不跳过、不按固定输入写死；并发与重启自行验证；只做本地实现。
