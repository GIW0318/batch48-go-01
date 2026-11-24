# 题面

视频会议在弱网下花屏，接收端连续发重传请求却收不到包，偶尔收到的还是十几秒前同一序号的老包，关键帧丢失后整段画面停在灰屏，值班同学抓包发现发送端的缓存条目早就被挤掉了，却没有任何计数能对上。问题在 gsb/batch48-go-game-c-10 的 rtp_retransmit_cache.go，RetransmitCache 在发送侧保留最近发出的 RTP 包以响应重传请求：按 SSRC 与扩展序号索引，包数与字节数双限，按 RTT 裁剪过期项，并按帧重要性决定恢复优先级。

公开接口：NewRetransmitCache(limits Limits) (*RetransmitCache, error)、Store(p Packet) error、Request(ssrc uint32, seq uint16, nowMs int64) (Packet, error)、Lookup(ssrc uint32, seq uint16) (Packet, bool)、Purge(nowMs int64) int、SetLimits(limits Limits) error、Pending() int、Bytes() int、Snapshot() (CacheState, error)、Restore(CacheState) error、Stats() CacheStats。Limits 的 MaxPackets、MaxBytes、MaxPacketBytes、MaxRetransmits 必须为正，CoalesceMs 不得为负；Lookup 只读索引、不消耗重传额度；Stats 分别累计入库数、重复入库数、淘汰数、过期数、重传数、合并数、未命中数与当前缓存字节数。

要修的十二处：空负载返回 ErrPayloadEmpty；单包超过 MaxPacketBytes 返回 ErrPacketTooLarge；同一 SSRC 与序号重复入库返回 ErrDuplicatePacket；包数或字节数超限时先按时间淘汰最旧的普通包，再考虑关键帧包，关键帧包之间同样按时间淘汰，不能永久驻留；查询与重传都按扩展序号解析，序号落在回绕点前后时不得让 16 位别名命中错误的历史包；不存在的序号返回 ErrNotFound 并计一次未命中；同一序号在 CoalesceMs 内重复请求只算一次重传，后续请求返回 ErrDuplicateRequest；单包重传次数达到 MaxRetransmits 返回 ErrRetransmitLimit；Purge 按 MinTTLMs 与四倍 RTT 中的较大值裁剪过期项并返回淘汰条数；SetLimits 收到非正限额返回 ErrLimitsUnusable；Snapshot 与 Restore 要带回缓存条目、各 SSRC 的 RTT 估计与统计；Bytes 返回当前缓存负载的总字节。缓存条目按 SSRC 分开索引，不同 SSRC 的相同 16 位序号互不影响；SetLimits 缩小限额后立即按新上限淘汰，返回时 Pending 与 Bytes 必须落在新上限内；Purge 只淘汰过期条目，不改变未过期条目的重传计数；缓存为空时 Purge 返回 0。同一 SSRC 的重复请求按序号定位到唯一条目，不同请求互不影响；Request 命中后返回的载荷与入库时逐字节一致；Purge 与容量淘汰都先处理已发出最久的条目，发送时刻相同的条目保持稳定顺序。

硬约束：Store 与 Request 会被发送与重传两条协程并发调用，索引必须保持一致，不允许读到半个包，也不允许同一包在一次窗口内被计两次重传；缓存总字节不得超过 MaxBytes，淘汰顺序可复现；重启恢复后同一序号的重传计数与合并窗口继续生效，不能因为恢复把已经用完的重传额度清零。

验收：go test ./... 的 15 条用例全部通过，不许改测试、不许跳过、不许按固定序号写死；并发存取与重启恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块

- 模块路径：gsb/batch48-go-game-c-10
- 包名：task
- 源文件：rtp_retransmit_cache.go
