# 撤销事件扇出与按节点水位补发

## 题面

凭证撤销后边缘节点仍放行，各节点补发水位对不上。落点 gsb/batch48-go-safety-a-04 的 revocation_fanout_ledger.go（RevocationFanoutLedger），签名：NewRevocationFanoutLedger(LedgerConfig) *RevocationFanoutLedger、Append(events []RevocationEvent) (uint64, error)、Ack(nodeID string, seq uint64) error、Pending(nodeID string) ([]uint64, error)、Backfill(nodeID string, to uint64) (int, error)、Snapshot() ([]byte, error)、Restore([]byte) error；LedgerConfig 的窗口、节点表与重试上限须生效。

要修：空批次或事件缺主体仍入账，须报 ErrEmptyBatch、ErrInvalidEvent 不耗序号；节点重复登记、空白 id、超表上限放行，报 ErrNodeExists、ErrBlankArg、ErrNodeLimit；水位会回退，重复确认须幂等且水位只前进；确认序号跳跃留下空洞也接受，须拒绝并要求重传；未登记节点也放行，须报 ErrUnknownNode 且 Watermark 返回 false；Pending 未从水位后一条列出缺失，追平后须为空；Backfill 未从节点水位起算也不回夹，须按水位到 min(to,最新) 计数且不改水位；重试超预算不降级，须降级报 ErrNodeDegraded 并拒绝后续补发，重新上线才恢复；节点降级后重新上线，补发按全局水位跳过它缺的区间，须从该节点自己的水位续补；事件早于窗口下界仍逐条补发，须标记该节点全量重同步；窗口超上限淘汰最旧且最新序号不回退；并发 Append 会分到重复号，须唯一；恢复丢节点水位与重试计数，须还原；坏快照报 ErrBadSnapshot 不动现网。

硬约束：并发 Append 串行分配，序号唯一；快照只见全量旧或新；事件窗口与节点表硬顶。

验收：go test ./... 15 条全过，测试不得改动、跳过、写死；并发与重启自验；仅本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-a-04`
- 实现文件：`revocation_fanout_ledger.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`RevocationFanoutLedger`
