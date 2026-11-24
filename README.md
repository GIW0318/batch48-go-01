# batch48-go-game-c-13

## 题面

院线片源接入转码集群后，24 帧的电影在 60Hz 屏上出现规律性顿挫，体育直播从 60 帧降到 30 帧时又整段跳帧，播放器日志里还能看到音画偏移越跑越大；有运维反馈，首帧还没解码出来播放器就已经排好了输出时刻，结果把第一帧重复了好几次；另一路信号源切换后帧序号回退，排程把旧帧当成新帧继续消费，画面直接乱序。请修 gsb/batch48-go-game-c-13 模块里的 pull_down_planner.go，类型 PullDownPlanner，包名 task，只允许使用标准库。

接口保持：NewPullDownPlanner(src, dst Rational) (*PullDownPlanner, error)、Configure(src, dst Rational) error、SetTargetRate(dst Rational) error、PushFrame(index int64) error、Next() (Decision, error)、SetPolicy(PlayoutPolicy) error、ExpectedOutputs(sourceFrames int64) int64、AccumulatedError() int64、Phase() int64、FramesAvailable() int64、Reset() error、Snapshot() ([]byte, error)、Restore([]byte) error、Stats() PlannerStats。Rational 含 Num、Den；PlayoutPolicy 含 MaxRepeat、MaxDrop；Decision 含 Output、Source、Repeat、Dropped；PlannerStats 含 Outputs、Repeats、Drops、RepeatOver、DropOver、Resets。

需要修的行为：

一、帧率必须为正，且 src.Num 乘 dst.Den、src.Den 乘 dst.Num 都要能装进有符号 64 位，溢出或非正返回 ErrRateRejected；失败的配置改动不得影响当前速率。

二、Next 对输出序号 n 用整数运算求源帧序号 floor(n * src.Num * dst.Den / (src.Den * dst.Num))，全程不使用浮点累加。

三、输出选中的源帧与上一输出相同则 Repeat 为真并计 Repeats；比上一输出前进超过一帧时 Dropped 给出跳过的帧数并累计 Drops。

四、源帧尚未到达时 Next 返回 ErrSourceUnderrun，不能凭空输出；首帧未到达前的输出时刻也不得凭空重复首帧，必须等首帧到达再开始排程；已经交付的输出槽位不得回退重排。

五、PushFrame 只接受非负序号；同一序号重复上报按幂等处理，不计新帧；比上一帧更小的序号视为源端重置，重置后从该帧重新开始排程。

六、同一源帧连续重复次数超过 MaxRepeat 时，每多一次计 RepeatOver；单次跳过超过 MaxDrop 帧时每次计 DropOver；未设置策略时按 2 和 2 处理。

七、SetPolicy 的两个阈值不得为负，否则返回 ErrPolicyRejected 并保留旧策略。

八、SetTargetRate 与 Configure 成功后输出相位归零、Resets 加一，源帧列表按新速率重新从输出 0 排程。

九、源帧序号回退视为重置并重新对齐相位，不能当成普通乱序帧继续按序消费。

十、ExpectedOutputs(sourceFrames) 按 n * dst.Num * src.Den / (dst.Den * src.Num) 的整数除法给出预计输出数；AccumulatedError 给出下一输出时刻落在源时间基上的小数余数，单位是 1/src.Den 帧，必须始终落在 0 到 src.Den 减一之间。

十一、快照保存两个速率、策略、输出相位、已收源帧水位与全部计数，恢复后从同一相位继续；坏数据 Restore 返回 ErrPlannerSnapshot 且不改现状。

十二、Reset 清空已收源帧并把相位归零。

并发与硬约束：PushFrame 与 Next 在同一把锁内串行，快照与推进互斥，快照只能看到完整相位；误差只用整数余数保存，不随输出数增长而膨胀；输出序号与源帧水位必须单调。

验收：go test ./... 的 15 条用例全过，不得改测试、跳过或按固定输入写死；并发推帧与重启续跑自行验证；只做本地实现。

## 模块与运行

- 模块路径：`gsb/batch48-go-game-c-13`
- 包名：`task`
- 实现文件：`pull_down_planner.go`
- 测试目录：`tests/`

```bash
go test ./...
```
