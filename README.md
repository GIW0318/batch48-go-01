# 任务包 batch48-go-game-a-27

## 模块

- module: gsb/batch48-go-game-a-27
- package: task
- file: patrol_alert_guard.go

## 题面

副本巡逻队改版后，守卫的反应完全不合预期。玩家从背后靠近，第一个守卫已经喊人，旁边的同组守卫却还在原地踱步；有人被打断巡逻后脱离战斗，回来却从路线起点重新走，像没打过一样。警戒等级也怪：刚进入战斗的守卫过一会儿直接回到巡逻态，中间的怀疑和搜索两级一闪而过；同组里已经高度戒备的守卫，收到低级求援后反而放宽了，队长再喊也不理。另一个现象是打了又退、退了又打，守卫的警戒忽高忽低，客户端小地图上的等级跟实际追击对不上。落点 gsb/batch48-go-game-a-27 的 patrol_alert_guard.go，类型 PatrolAlertGuard，签名：NewPatrolAlertGuard(Options) *PatrolAlertGuard、Assign(npc string, route []Point) error、Sense(npc string, ev SenseEvent, at int64) (Alert, error)、Step(npc string, at int64) (Move, error)、CallHelp(npc string) ([]string, error)、Decay(npc string, at int64) Alert、AlertOf(npc string) Alert、Route(npc string) []Point、Position(npc string) (Point, bool)、Snapshot() ([]byte, error)、Restore([]byte) error。

要修：
一、Assign 遇到空名报 ErrInvalidNPC，路由为空报 ErrEmptyRoute，同一 NPC 重复分配报 ErrDuplicateNPC。
二、Step 对未登记 NPC 报 ErrUnknownNPC；正常推进每步最多走 Options.Speed 格，先补 X 再补 Y，Move 带回起点、终点、目标路点序号与是否停留。
三、到达路点后进入停留，时长为 Options.HoldTicks；停留期内 Step 原地返回且 Holding 为真，期满后转向下一个路点，序号按路由长度回绕。
四、Sense 只接受 sight 与 sound 两种事件，其它种类或负距离报 ErrInvalidEvent；未登记 NPC 报 ErrUnknownNPC。
五、sight 在 SightRange 内升一级警戒并记录目标与时刻，超出范围不算错误、保持原状，目标字段不得填。
六、sound 在 SoundRange 内同样升一级，但最多升到 2 级，不能靠声音直接进入战斗；sight 最高升到 3 级。
七、升到 3 级即进入战斗，自动向同组求援并把未到 2 级的成员抬到 2 级；Step 不再移动，原地返回并置 Holding；CallHelp 返回已响应的同组 NPC 名，按字典序排列，组外的不得混入。
八、战斗等级以下调用 CallHelp 报 ErrNotInCombat，未登记 NPC 报 ErrUnknownNPC。
九、警戒回落要逐级下降，并在每一级停留 DecayTicks 指定的时长，不能从最高级一次跳回巡逻态；Decay 对未登记 NPC 返回零值 Alert 而不是 panic，对巡逻态 NPC 多次调用等级保持 0，身份字段照常回填。
十、同组 NPC 已处于更高警戒时，收到低级别广播只能保持不降级，不能被覆盖成低级；组内广播只抬不压。
十一、巡逻被战斗打断后要记住被打断的路点与朝向，脱战后从该点继续，不能从头开始。
十二、Snapshot 覆盖路由、位置、路点序号、停留窗口、警戒表与响应名单；Restore 遇坏数据报 ErrBadSnapshot 且原状态保留，恢复后 Step 与 Sense 都能接着推进。

硬约束（并发与原子性）：同一 NPC 的 Sense 与 Step 可并发，但警戒等级只能单向推进到本轮最高，不得出现半级；同组广播与本地升级对同一张警戒表必须原子合并，组内成员看不到写了一半的状态。

硬约束（快照与重启）：快照与写入互斥，恢复失败时原有警戒表与路点必须原样保留；重启后停留窗口、警戒时刻与被打断的路点都要能继续推进。

验收：go test ./... 15 条全过，不改测试、不跳过、不按固定输入写死；并发与重启自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```
