# batch48-go-game-c-04

## 题面

连麦房间的观众一直反馈画面糊成一片：三个人同时点请求关键帧，上游一次性收到三条重复请求，编码器被打得掉帧；更奇怪的是同一个人手快连点两次，第二次也被当成新请求转发出去。另有一个观众在节流窗口里被拒之后，窗口过去很久再点依然被拒，像是惩罚一直累积。还有人反馈关键帧明明到了，界面上的等待提示却一直不消失。请修 gsb/batch48-go-game-c-04 模块里的 keyframe_request_coalescer.go，类型 KeyframeRequester，包名 task，只允许使用标准库。

接口保持：NewKeyframeRequester(Config) *KeyframeRequester、Request(Request) error、Upstream(int64) (UpstreamRequest, bool)、OnKeyframe(string, uint64) int、OnSubscriberLeave(string) int、Pending() int、PendingFor(string) int、Snapshot() ([]byte, error)、Restore([]byte) error、Stats() RequesterStats、Reset() error。Request 含 Subscriber、Stream、Key、StartSeq、NowMs；UpstreamRequest 含 Stream、Seq、Requesters、Priority、Reason、DeadlineMs；Config 含 ThrottleMs、TimeoutMs、MaxPendingPerStream、MaxPendingTotal；RequesterStats 含 Requests、Coalesced、Throttled、LimitRejected、Upstream、Satisfied、Escalated、Cancelled、Pending。

需要修的行为：

一、Request 校验订阅者、流与幂等键，三者任一为空分别返回 ErrRequestField 或 ErrKeyRequired，拒绝时不写入任何状态；Requests 只统计被接受的请求数。

二、幂等键重复的请求只记 Coalesced，不新增未决项，也不产生第二次上游请求；幂等判断先于节流判断。

三、同一订阅者对同一流在 ThrottleMs 窗口内再次请求返回 ErrThrottled，Throttled 加一，被拒请求不占未决名额。

四、订阅者在节流窗口内被拒之后，窗口结束的首次请求必须立即放行，不能延续任何累积惩罚。

五、同流多个订阅者的请求合并为一次上游请求，Requesters 记录合并人数；不同流各自独立，互不合并。

六、Upstream 返回最该发送的那条未决请求并标记已派发，未到超时时间再次调用不重复返回同一条；同时有多条可发时按最早请求时间、同一时刻按流名升序；Seq 从 1 开始按派发次数递增，Reason 在普通派发时为 coalesced，升级派发时为 escalated。

七、派发后达到 TimeoutMs 仍未满足，下一次 Upstream 返回同一条并升到更高优先级，Escalated 只记一次。

八、OnKeyframe 只满足同一条流上序号晚于请求起点的未决项，返回本次满足数量并累加 Satisfied，其他流不受影响。

九、关键帧到达但序号早于请求发起时刻不得标记满足，必须继续等待更新的关键帧。

十、OnSubscriberLeave 撤销该订阅者全部未决项并计入 Cancelled，撤销的项不得计入 Satisfied；未知订阅者返回零不报错。

十一、每流未决项数量受 MaxPendingPerStream 限制，总量受 MaxPendingTotal 限制，超限返回 ErrPendingCap 并计 LimitRejected，限额按流独立计算。

十二、快照包含订阅者节流时间、未决项与幂等键、派发序号与统计；恢复后 Pending、PendingFor 与下一次 Upstream 行为一致。

并发与原子性：Request、Upstream、OnKeyframe、OnSubscriberLeave 并发时同一流的未决状态只能被一个调用看到，同流一次只派发一条上游请求。快照与重启：Snapshot 保存全部可恢复状态，Restore 坏数据返回 ErrStateCorrupt 且不改动内存状态。验收：go test ./... 的 15 条用例全过，不得改测试、跳过或按固定输入写死；并发与重启自行验证；只做本地实现。

## 模块与运行

- 模块路径：`gsb/batch48-go-game-c-04`
- 包名：`task`
- 实现文件：`keyframe_request_coalescer.go`
- 测试目录：`tests/`
- 运行全部用例：`go test ./...`
