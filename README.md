# 有状态 TCP 代理的会话接管：按偏移重放与旧连接 fencing

## 题面


续接令牌与会话表是防连接劫持的边界；攻击者复用令牌或重放旧确认，就能把客户端数据流接到自己的连接上。凌晨的故障单里，一条批量上传链路在客户端换过源端口之后彻底断掉：代理侧找不到原会话，上游仍在继续发送，客户端只能整段重传，重传的字节又和上游已经发出的数据错位。值班同学还遇到更糟的情况：同一个续接令牌被两条新连接同时使用，老连接迟到的确认把新连接的重放位置推回去，重启之后客户端收到的第一段数据从中间开始。压测时会话表被一次性灌满，登记失败的会话仍旧占着令牌，真正要重连的客户端反而拿不到入口。

落点 gsb/batch48-go-safety-a-17 的 stream_takeover_ledger.go，类型 TakeoverLedger，构造函数 NewTakeoverLedger(Policy) *TakeoverLedger，Policy 携带 MaxSessions、MaxReplayBytes 与 TokenTTL。对外签名保持：Register(sessionID, token string) error、Takeover(token string, conn Conn) (*Session, error)、Ack(sessionID string, offset uint64) error、Fence(sessionID string)、Append(sessionID string, payload []byte) (uint64, error)、Replay(sessionID string, from uint64) ([]byte, error)、Session(sessionID string) (*Session, bool)、Sessions() []string、Stats() LedgerStats、Snapshot() ([]byte, error)、Restore([]byte) error；Takeover 返回的 *Session 另有 Ack(offset uint64) error 与 Replay(from uint64) ([]byte, error)。

要修的地方：会话标识或令牌为空时 Register 报 ErrInvalidArgument，重复会话报 ErrDuplicateSession，超过 MaxSessions 报 ErrSessionLimit，令牌已被别的会话绑定不得复用；Takeover 对未知令牌报 ErrUnknownToken、对空连接报 ErrNilConn，同一令牌同时只允许一条活动连接，接管成功后旧句柄立即变为只读；Append 的空负载报 ErrEmptyPayload，未确认窗口加新负载超过 MaxReplayBytes 报 ErrReplayFull，返回值是新的上游已发偏移；Ack 超过已发偏移报 ErrAckBeyondSent，低于已确认偏移报 ErrStaleAck 并计入陈旧确认计数，等于已确认偏移按幂等成功处理；Replay 从已确认点裁剪，from 大于已发偏移报 ErrReplayGap，返回的字节不能被调用方改动内部缓冲；Fence 之后 Append 报 ErrFenced，必须再次 Takeover 才能继续写；快照要还原已发偏移、已确认偏移、重放窗口、连接代数与统计计数，恢复后可以继续 Append；损坏快照报 ErrBadSnapshot 且不改动现有状态；Sessions 按字典序返回会话标识，Stats 返回计数副本，两者都不得把内部切片或映射直接交给调用方。旧连接进入只读状态后迟到的 Ack 只能计入丢弃计数，不得推进上游确认点，也不得让新连接重发已确认的字节。令牌过期与令牌已被其他连接占用必须返回不同错误码，并分别计入审计计数。快照只覆盖部分会话时，被截断会话的待重放缓冲要整段丢弃，不得以半截区间恢复后向客户端补发。

硬约束：会话表、令牌索引、重放窗口与计数共用一把互斥锁，并发 Takeover 只能有一个成功，读方不见半更新；每条会话的未确认窗口有硬上限；快照必须保证已确认偏移不大于已发偏移、窗口长度与两者之差一致，恢复后重放区间不留空洞也不重复投递。

验收：go test ./... 15 条全过，测试不得修改、跳过或按固定输入写死；并发与重启自行验证；只做本地实现。以上现象均在真实生产流量下复现。
## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-a-17`
- 实现文件：`stream_takeover_ledger.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`TakeoverLedger`、`Session`、`Conn`、`Policy`
