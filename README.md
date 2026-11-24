# 任务包 batch48-go-game-a-18

## 模块

- module: gsb/batch48-go-game-a-18
- package: task
- file: session_clock_estimator.go

## 题面

排位赛的时钟同步面板这几周一直报异常：某条线路抖动后，单次往返时延飙到四百毫秒，客户端立刻把整段回放拖慢两秒，之后一直没恢复；跨区路由调整后，单向延迟持续增长，偏移估计却像被钉死一样停在旧值，玩家看到的是越来越大的位置拉扯；服务器机房做时间校准，时间戳整体前跳之后，同步一直被判成非法，重连也没用。值班同学把采样日志导出来对比，发现同一个会话里读数还会在两次面板刷新之间来回跳，指针忽快忽慢。落点 gsb/batch48-go-game-a-18 的 session_clock_estimator.go，类型 SessionClockEstimator，签名保持：NewSessionClockEstimator(window int) *SessionClockEstimator、Sample(send, recv, serverMs int64) error、Offset() int64、RTT() int、Reset()、SetWindow(n int) error、Samples() int、Rejected() uint64、Stats() EstimatorStats、Snapshot() ([]byte, error)、Restore(payload []byte) error。

要修：每次采样用 serverMs 减去 send 与 recv 的中点得到偏移样本，中点按整数除法向下取整；偏移取窗口内样本的中位数，单次异常样本不能立刻拉走结果，估计值只在样本进出窗口时改变；RTT 为 recv 减 send，窗口内同样取中位数用于超时判定；样本数为偶数时中位数取两个中间值中较小的那个，偏移与 RTT 两端同口径，不能一端取平均；窗口只保留最近 n 个样本，最旧的样本退出后估计值随窗口滑动更新，窗口内样本保持采集顺序，重算时不能打乱同一帧多条样本的先后；recv 不大于 send 的样本返回 ErrInvalidSample，不进入窗口但计入 Rejected；send 或 recv 比上一次有效采样更小的样本按本地时钟回拨返回 ErrClockRollback，同样不入窗口；SetWindow 拒绝小于 1 的窗口，缩窗时只保留最新的若干个样本；Reset 清空样本与估计，重连后的新会话从零开始，旧样本不得带入；采样持续单向延迟增长时，估计值要在一个窗口内跟随新样本，不能被旧中位数永久锁死；服务器时间真实跳变要由连续一致样本确认后接受，确认前持续拒绝，不能一直拒绝；快照保存窗口样本与当前估计，恢复后新旧会话的样本不能混在一起，空载荷或损坏载荷返回 ErrBadSnapshot。

硬约束：Sample 与 Offset、RTT、Stats 可并发调用，窗口替换与估计更新必须在同一临界区内完成，读者永远看到自洽的样本数与估计，不允许先读到新样本数再读到旧估计；窗口长度硬性封顶，任何路径都不得让样本数超过窗口大小；快照必须自包含，恢复后的窗口不得包含快照之外的样本。

验收：go test ./... 15 条全过，不得改测试、跳过或按固定输入写死；并发与重启恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```
