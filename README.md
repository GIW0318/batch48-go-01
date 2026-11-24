# batch48-go-game-c-03

## 题面

海外机房回传的直播流一到晚上就抽风：码率控制器每隔几秒把目标码率顶到上限，几十秒后又猛砍到底线，观众端一会儿缓冲一会儿马赛克。运维把上限从 3 Mbps 调到 1.5 Mbps 应急，结果控制器完全没反应，仍然按旧上限发流，出口被打满。还有人发现每次状态切换之后立刻又切回去，来回抖，抓出来的记录里冷却像是没生效。压测同事复现时还发现，进程重启以后冷却状态直接归零，刚恢复就又把码率顶了上去。请修 gsb/batch48-go-game-c-03 模块里的 bandwidth_estimator.go，类型 BandwidthEstimator，包名 task，只允许使用标准库。

接口保持：NewBandwidthEstimator(Bounds) (*BandwidthEstimator, error)、AddSample(Sample) error、Target() int、SetBounds(int, int) error、EnterCooldown(int64) error、CooldownRemaining() int64、State() string、Snapshot() ([]byte, error)、Restore([]byte) error、Stats() EstimatorStats、Reset() error。Sample 含 GroupID、SendMs、ArrivalMs、Bytes、Received、Lost；Bounds 含 MinBitrate、MaxBitrate、StartBitrate、IncreaseStep、DecreaseFactor、GradientThreshold、OverloadHoldMs、LossThreshold、CooldownMs、WindowSize；EstimatorStats 含 Samples、BadSamples、Duplicates、Overuse、Underuse、Increases、Decreases、Clamped、LossGated、CooldownBlocks、StateSwitches。

需要修的行为：

一、构造时校验 MinBitrate 为正、MaxBitrate 不小于 MinBitrate、StartBitrate 落在区间内、IncreaseStep 为正、DecreaseFactor 在零到一之间、WindowSize 至少为一，违规返回 ErrBoundsInvalid。

二、AddSample 校验到达时间晚于发送时间、Bytes 与 Received 为正、Lost 非负，违规返回 ErrSampleRejected 且不计入窗口；GroupID 重复只记 Duplicates，不重复参与梯度计算。

三、排队延迟梯度等于相邻两组的到达间隔减发送间隔，窗口内取中位数，避免单个尖峰带偏；中位数只统计最近 WindowSize 组，窗口未满时用已有样本。

四、一次尖峰超过 GradientThreshold 不算过载，必须连续超阈值且持续时长达到 OverloadHoldMs 才判定过载。

五、过载分级后下降，目标按 DecreaseFactor 收缩；欠载持续同样时长后上升，每次只加 IncreaseStep，上升速率必须慢于下降速率，状态取值只有 hold、increase、decrease 三种。

六、丢包率按窗口内 Lost 与总包数之比计算，超过 LossThreshold 时只许降不许升，被拦下的上升计入 LossGated。

七、每次状态切换后进入 CooldownMs 冷却，冷却期内不再切换并计 CooldownBlocks，剩余冷却可以通过 CooldownRemaining 查询。

八、冷却剩余必须随快照一起保存并在恢复后继续计时，恢复时清零会让码率提前回升，再次把链路打满。

九、目标码率任何时候都夹在 MinBitrate 与 MaxBitrate 之间。

十、外部调低上限时目标立即夹紧，同时要记一次受限下降，以便把策略限制和真实拥塞区分开。

十一、EnterCooldown 校验非负，设置后按样本到达时间递减，冷却未走完不得切换状态。

十二、相同样本序列重复喂入结果确定，重复 GroupID 不改变窗口；AddSample、Target、SetBounds 并发时只看到完整窗口。

快照与重启：Snapshot 保存梯度窗口、状态、目标码率、冷却剩余与统计，Restore 后 Target、State、CooldownRemaining 与快照时一致，坏数据返回 ErrStateCorrupt 且不改动内存状态。验收：go test ./... 的 15 条用例全过，不得改测试、跳过或按固定输入写死；并发与重启自行验证；只做本地实现。

## 模块与运行

- 模块路径：`gsb/batch48-go-game-c-03`
- 包名：`task`
- 实现文件：`bandwidth_estimator.go`
- 测试目录：`tests/`
- 运行全部用例：`go test ./...`
