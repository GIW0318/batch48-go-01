# TLS 1.3 终结代理的 KeyUpdate 纪元跟踪

## 题面

TLS 1.3 终结代理上线后，长连接每隔一段时间就被对端重置一次，抓包里能看到 KeyUpdate 之后紧跟的几条 record 被代理按旧纪元处理，直接触发了断连；另有连接在重启后对同一个 update_requested 又回了一次 KeyUpdate，客户端因此认为密钥状态错乱。运维还发现，某个客户端连续发送 KeyUpdate 时，代理回了多份应答，把两边的纪元推得不一致；更麻烦的是，代理把应答写在了旧纪元上，客户端按新纪元校验后直接丢弃，双方各自重传，连接卡在原地直到超时。

落点 gsb/batch48-go-safety-a-18 的 tls_keyupdate_epoch_tracker.go，类型 KeyUpdateTracker，构造函数 NewKeyUpdateTracker(KeyUpdateConfig) (*KeyUpdateTracker, error)，配置里放 MaxEpochs 与 EpochTolerance。对外签名保持：ObserveRecord(dir Direction, epoch uint16, seq uint64) error、RequestUpdate(dir Direction) error、OnKeyUpdate(dir Direction, requested bool) (bool, error)、EpochOf(dir Direction) uint16、NextSeq(dir Direction) uint64、ResponseEpoch(dir Direction) (uint16, error)、MarkResponseSent(dir Direction) error、MarkUpdateSent(dir Direction) error、Stats() KeyUpdateStats、Snapshot() ([]byte, error)、Restore([]byte) error。方向常量是 DirClientToProxy 与 DirProxyToClient，Direction 上有 Opposite() 与 String()。

要修的地方：两个方向的纪元各自从 1 开始独立递增，一侧 KeyUpdate 不得改动另一侧的 record 序号；同一纪元内 record 序号必须连续，重复或跳号报 ErrReplayRecord 并计入重放计数；record 的纪元比当前大一步以上报 ErrEpochGap，必须等 KeyUpdate 推进纪元后才接受；落后于当前纪元但在 EpochTolerance 之内的 record 计数放行，超出窗口报 ErrEpochTooOld；KeyUpdate 推进纪元后该方向序号重置为 0，新纪元第一条 record 必须是 0；收到 update_requested 必须报告要回一次应答，应答写出后调用 MarkResponseSent 清除待应答状态，没有待应答时调用报 ErrNoPendingResponse；本地发起 KeyUpdate 用 RequestUpdate，连续调用只保留一次并计入合并计数，写完后用 MarkUpdateSent 清除，重复清除报 ErrNoPendingUpdate；纪元达到 MaxEpochs 后任何推进都报 ErrEpochExhausted 并把连接置为关闭，此后 record 与请求一律报 ErrClosing；非法方向在所有入口报 ErrInvalidDirection，方向字符串只用于日志展示，不参与状态判断；Stats 返回计数副本，调用方改动返回值不得影响内部计数；快照要保存两个方向的纪元、序号、待应答标记、应答所用纪元与待写标记，恢复后序号接续、不得重放已处理 record；损坏快照报 ErrBadSnapshot 且不改动现有状态。同一批 record 中新纪元先到、旧纪元后到时，要按到达顺序分别计数，不能把旧纪元一律当攻击断开。对端连续发送 update_requested 而本地应答尚未写出时只能合并回一次，且应答必须落在新纪元。快照恢复后若已应答标记丢失，不得对同一 update_requested 再回一次。

硬约束：每连接一个互斥锁保护两个方向的纪元、序号与计数，ObserveRecord 与 KeyUpdate 处理不得交错出半更新状态；待应答与待写标记必须与纪元同批提交，不能先改纪元再补标记；纪元数达到上限后按优雅关闭处理，不再接受新 record。

验收：go test ./... 15 条全过，测试不得修改、跳过或按固定输入写死；重启与并发自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-safety-a-18`
- 实现文件：`tls_keyupdate_epoch_tracker.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`KeyUpdateTracker`、`Direction`、`KeyUpdateConfig`、`KeyUpdateStats`
