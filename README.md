# CONNECT-UDP 代理的 capsule 流式解析与 flow 绑定

## 题面

夜间一批 UDP 代理连接被对端整条重置，抓包显示同一连接上前一个 capsule 尾部与后一个 capsule 头部粘在一起；另有客户端把同一条回复收到两次，重启后又一次。值班同学还遇到三种情况：半帧被当成整帧投递，后面的帧全部错位；length 被按定长解析，正常帧被判成超长直接断连；排空阶段新建的 flow 被放进来，关闭时队列里的回复又被打包重发一遍。这一版要在同一条连接上完成 capsule 拆帧、flow 绑定、出站入队与排空收尾，任何一帧都只能被消费一次。落点 gsb/batch48-go-safety-a-16 的 masque_capsule_router.go，类型 CapsuleRouter。签名保持：NewCapsuleRouter(CapsuleLimits) *CapsuleRouter、Feed([]byte) error、OpenFlow(uint64) error、CloseFlow(uint64)、Deliver(uint64, []byte) error、Drain(time.Time) error、Snapshot() ([]byte, error)、Restore([]byte) error。

要修：capsule 的 type 与 length 都按 varint 解析，同一批里多帧按到达顺序全部消费，跨批半帧留到下次拼接，不能丢字节也不能提前投递，半个帧头不得产生 datagram 也不得计入入站统计；单帧 length 超 MaxCapsuleBytes 报 ErrCapsuleTooLarge，ProtocolErrors 加一，并停止该连接继续解析；未知 type 跳过并计入 UnknownCapsules，padding 不产生数据也不影响后续帧，空的 chunk 不改变状态也不报错，连续空批不得伪造 datagram；数据帧首部是 varint flow id，未登记 flow 的 datagram 丢弃并计入 DatagramsDropped，不得隐式建 flow；OpenFlow 对 0、重复、超 MaxFlows、排空中分别报 ErrZeroFlowID、ErrDuplicateFlow、ErrFlowLimit、ErrDraining，打开成功后该 flow 的 Pending 为 0/0，FlowIDs 按升序返回当前连接且不含已关闭的 flow；Deliver 对未知 flow、超大 payload、队列满分别报 ErrUnknownFlow、ErrPayloadTooLarge、ErrQueueFull，入队帧带 flow id 前缀，payload 按原字节入队，超限只在入队前判断，DatagramsOut 只统计成功入队的回复，被拒的 payload 不得占队列；入站 datagram 按 flow 独立编号从 1 开始，TakeInbound/TakeOutbound 取走即清空并按到达顺序返回，CloseFlow 同时丢弃双向队列，关闭后 Deliver 报 ErrUnknownFlow、Pending 归零；Drain 拒绝新 flow 但已有 flow 仍可 Deliver，截止已过且待发队列非空报 ErrDrainTimeout 并累加 DrainTimeouts，清空后同一截止返回成功，排空标记要保留；长度为 0 的数据帧与 flow id、length 的非最短 varint 一律返回 *ProtocolError 并带上可确定的 flow 号与偏移，不得当空帧跳过，ProtocolErrors 相应累加；GOAWAY 之后仍在排空的 flow 再收到 datagram 只计入 RejectedAfterGoAway 并丢弃，不得重建同名 flow，GoAwayCapsules 记录收到的 GOAWAY 数量；连接关闭时读缓冲里的半帧随快照一并丢弃，恢复后不得补齐成整帧投递；Stats 返回计数器副本，调用方改动副本不得影响连接的后续统计；损坏快照报 ErrBadSnapshot 且不改现有状态。

硬约束：Feed、OpenFlow、Deliver、Drain、Take 与快照共用一把互斥锁，读方不见半更新；flow 数、双向队列深度、单帧与单 datagram 字节数全部硬顶，超出按错误返回而不是覆盖或截断；快照还原 flow 表、每 flow 序号、双向队列、排空标记与计数，已取走的出站帧不得重发，重启后序号从原值继续。

验收：go test ./... 15 条全过，测试不得修改、跳过、写死；并发与重启自验；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-a-16`
- 实现文件：`masque_capsule_router.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`CapsuleRouter`
