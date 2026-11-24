# batch48-go-safety-d-05

## 题面

发布流水线在周五晚高峰连续失败：同一批制品重签之后，回执里的制品摘要和 provenance 摘要对不上，构建团队与安全团队各执一份，谁也说不清哪一份才是被签过的。密钥泄露应急时按分区批量撤销，撤销做到一半有新的重签请求插进来，给已经撤销的制品又发了一张有效回执；等撤销结算完成，重签台账里少了两条记录。密钥分区升级到新代际之后，旧代际的回执查不到了，重签落到哪一代也没有依据。配额也是糊涂账：某分区配额早就用尽，回执照发，另一个分区却提前报配额不足。进程重启后从快照恢复，分区代际、配额余量和撤销状态全部回到初始值。落点 gsb/batch48-go-safety-d-05 的 artifact_signing_service.go，类型 SigningService，受理制品签名时校验制品摘要与 provenance 摘要的双向绑定，按环境选择密钥分区，扣减签名配额并生成带算法与代际的回执。

接口保持：NewSigningService() *SigningService、RegisterKey(spec KeySpec) error、Sign(req SignRequest) (Receipt, error)、BeginRevocation(partition Environment, generation int) (RevokeReport, error)、SettleRevocation(partition Environment) (RevokeReport, error)、ResignBatch(originalIDs []string) (ResignReport, error)、QueryReceipt(signID string) (Receipt, error)、SetQuota(partition Environment, quota int) error、QuotaRemaining(partition Environment) (int, error)、PartitionStatus(partition Environment) (PartitionStatus, error)、Generation(partition Environment) (int, error)、Ledger() []LedgerEntry、Snapshot() (SigningSnapshot, error)、Restore(snap SigningSnapshot) error、Stats() SigningStats。Environment 只接受 prod、staging、dev；摘要统一写成 sha256: 加 64 位小写十六进制；Provenance 带 ProvenanceDigest、ArtifactDigest、Builder；Receipt 带 SignID、ArtifactDigest、ProvenanceDigest、Environment、Generation、Algorithm、Revoked、ResignOf；ResignReport 带 Requested、Resigned、Skipped、AlreadyResigned、Failed、NewSignIDs；LedgerEntry 带 Seq、Kind、SignID、Generation，Kind 依次取 key_registered、signed、revocation_begun、revocation_settled、resigned。

要修的行为：（1）环境写成 qa 也照签，分区按字符串前缀猜，prod-backup 被当成 prod。（2）制品摘要只校验长度不校验 sha256 前缀与十六进制，provenance 摘要为空也能签。（3）provenance 里记录的制品摘要与请求不一致时仍然签，双向绑定形同虚设。（4）分区配额不扣减，配额耗尽照样出回执；配额下调到已用量以下也不报错。（5）回执里的代际与算法取自全局默认值，而不是该分区注册的那一代。（6）撤销按分区但不按代际，新代际的制品被老代际的撤销令一起作废。（7）撤销分两步，中间窗口不设防，重签直接插队，给已撤销制品发出有效回执。（8）重签不引用原签名 ID，同一原件重复重签会生成多张回执，重签也不幂等。（9）重签落到旧代际，旧回执的引用关系丢失。（10）台账按 map 遍历输出，发生顺序错乱，重启后条目序号重复。（11）快照不含撤销状态与配额余量，恢复后已撤销制品重新变成有效。（12）查询不存在的回执返回空结构而不是明确错误。

硬约束：撤销结算期间到达的重签请求必须等待结算完成，不得插队产生已撤销制品的有效回执；重签以原签名 ID 幂等，落在分区当前代际并保留对旧回执的引用；台账追加顺序与发生顺序一致且序号从 1 起连续；快照恢复后分区代际、配额余量与撤销状态不得回弹。

验收：go test ./... 15 条全过，不得修改测试、不得跳过、不得按固定输入写死；撤销结算与重启恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块路径与包名

- module: gsb/batch48-go-safety-d-05
- 根目录包名: task（artifact_signing_service.go）
- 测试目录: tests（package task）
- Go 1.23，仅标准库
