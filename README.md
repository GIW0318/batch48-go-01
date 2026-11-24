# 双向中继的信用翻译与共享缓冲

## 题面


信用桥是防超发与死锁的边界；攻击者不归还信用或制造半关闭，就能让中继持续超发并拖垮两端。跨机房中继上线第一周出了三次事故：双向同时压测时两端一起卡死，运维把下游确认追平也没恢复，只能重启进程；一侧半关闭后，另一侧还在等确认的数据被中继一起丢掉；还有一次下游确认回得很快，上游信用没归还，中继却继续放行，超发了三兆字节被对端整段丢弃。落点 gsb/batch48-go-safety-a-25 的 bidirectional_credit_bridge.go，类型 CreditBridge，签名保持：NewCreditBridge(lim Limits) *CreditBridge、Attach(up, down Link) error、Grant(dir Direction, bytes uint64) error、Consume(dir Direction, bytes uint64) error、Ack(dir Direction, seq uint64) error、HalfClose(dir Direction) error、Drain() error、Credit(dir Direction) uint64、Inflight(dir Direction) uint64、PoolFree() uint64、Snapshot() ([]byte, error)、Restore(payload []byte) error。Direction 只有两个合法取值，Link 提供 Pause 与 Resume。

要修：未连接上下游就放行或确认，重复连接也不报错；信用授予的零值、未知方向、超过上限三种输入都能通过；单次放行超过对端信用窗口仍然成功；停等链路已有未确认数据时继续放行新序号；确认不校验序号，错序号也能清掉未确认字节，重复确认把共享缓冲释放两次；两个方向各自记账，合计占用能超过缓冲池容量；缓冲不足时不按方向公平分配，先到的方向把整个池吃满，另一个方向永远拿不到；对端窗口收缩时把已经发出的数据回退，或者继续按旧窗口放行；半关闭的方向仍能放行新数据，半关闭标记重复设置时报错含义不明，半关闭还把另一方向一起停掉；下游连续确认但上游信用尚未归还时，中继用未来信用提前放行；快照只存信用不存未确认字节、序号与半关闭标记，恢复后重复放行已确认数据；恢复失败时把现有信用与未确认字节改坏；池容量或信用上限配置为零时不报错，按零值静默放行；Drain 不等待未确认数据就直接返回。

硬约束：Attach、Grant、Consume、Ack、HalfClose、Drain 与快照操作在同一把锁下串行，信用扣减、未确认字节与序号推进必须原子，两个方向并发放行时缓冲池合计占用不得超过上限；每一方向的在途字节不得超过对端信用窗口，停等链路一次只允许一个未确认序号，确认只能释放该序号对应的字节；两个方向共享同一个缓冲池，缓冲不足时按方向公平分配，任一方不得长期独占；半关闭只影响本方向的新放行，另一方向的信用翻译继续工作；快照要保存两个方向的信用、未确认字节、序号与半关闭标记，恢复要么整体生效要么完全不生效；下游暂停与恢复只由可用信用驱动，确认本身不归还信用。

验收：go test ./... 15 条全过，不得改测试、跳过或按固定输入写死；并发与重启恢复自行验证；只做本地实现。以上现象均在真实生产流量下复现。
## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-a-25`
- 实现文件：`bidirectional_credit_bridge.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`CreditBridge`、`Direction`、`Link`、`Limits`
