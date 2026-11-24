# batch48-go-game-a-31

- 模块路径：`gsb/batch48-go-game-a-31`
- 包名：`task`
- 实现文件：`escort_quest_guard.go`
- 测试目录：`tests/`

## 题面

护送玩法在赛季更新后连续收到投诉：队伍全程贴着 NPC 走，最后一段却被判失败；有人掉线几分钟再回来，任务已经结算成失败，重试后还要从第一段重走；同一路线两队同时接任务时，后一队的进度会踩到前一队的路点。请修 gsb/batch48-go-game-a-31 模块里的 escort_quest_guard.go，类型名为 EscortQuestGuard，包名 task，只用标准库。

对外接口必须保持这些签名：NewEscortQuestGuard(Options) *EscortQuestGuard、Start(questID, party string, at int64) (Escort, error)、Advance(escortID string, dist int, at int64) (Stage, error)、Join(escortID, player string, at int64) error、Leave(escortID, player string, at int64) error、Fail(escortID, reason string, at int64) error、Retry(escortID string, at int64) (Escort, error)、Tick(escortID string, at int64) error、SetCombat(escortID string, on bool, at int64) error，以及 Members、Away、Stage、Elapsed、Status、Snapshot、Restore。Options 带 Quests 任务定义表、EscortRadius、GraceMillis 与 CombatSpeedPercent；party 是逗号分隔的成员名单。

要落到的行为共十二条：

一、Start 校验 questID 在 Quests 中存在，否则返回 ErrUnknownQuest；party 去空白后为空返回 ErrNoParty；成员按名单顺序进入 Members，返回的 Escort 带非空 ID。

二、每段 Required 取 RouteLength/Stages 的整除值，Advance 只累加当前段，达到 Required 即进入下一段，跨段多余位移丢弃；Stage 报告当前段的 Index、Progress、Required、Completed，整条路线走完时最后一段 Progress 等于 Required 且 Completed 为 true，其余情况 Completed 为 false，同时 State 置 completed。

三、距离为负返回 ErrInvalidDistance；completed 或 failed 的实例再 Advance 返回 ErrEscortFinished；未知实例返回 ErrNoSuchEscort。

四、SetCombat 打开时有效位移为 dist*CombatSpeedPercent/100 向下取整，关闭后恢复原距离；战斗标记只影响推进速度，不影响宽限计时。

五、新实例 StageIndex 为 0、State 为 active、ElapsedMillis 为 0；Elapsed 按 at 减开始时刻计算，失败时冻结在失败时刻。

六、Leave 只把成员标成掉队，Members 仍保留该名字，Away 按名单顺序返回掉队者；对非成员调用返回 ErrNotInEscort。

七、Tick 逐个检查掉队时长，任何一人超过 GraceMillis 就把实例置 failed、FailReason 记为 grace-expired 并返回 nil；只有实例不存在才返回错误。

八、Fail 只作用于 active 实例，记录 reason 并冻结已耗时间，Status 能看到 failed 状态与原因；Retry 只接受 failed 实例，回到 active 且停在失败所在段，ElapsedMillis 保留原值并在之后继续累加。

九、Join 追加成员，重复加入返回 ErrAlreadyJoined；Members 与 Away 返回副本，外部改动不影响内部状态。

十、掉队宽限按人累计：短暂离队后归队不能直接判失败，但同一段内反复离队不能把已经消耗的宽限清零来无限续期。

十一、一个玩家同时只能待在一个护送里；加入第二个护送时先自动退出原护送并留下退出记录，避免两条实例都把他算在队里。

十二、失败重试只重置失败段及其之后的路点，已经完成的段不要求重走，也不能重复触发该段的奖励结算。

硬约束：Join、Leave、Advance、Tick 并发时必须用同一把锁把成员集合与宽限计时一起更新，不能出现人还在队里却已开始掉队计时；Snapshot 要覆盖任务定义、实例进度、成员、掉队计时与冻结耗时，Restore 到用空 Options 新建的 guard 后行为与中断前一致，损坏字节返回 ErrBadSnapshot；重复 Fail、重复 Retry、重复 Join 不改变已结算结果。

验收口径：go test ./... 的 15 条用例必须全部通过；不得修改测试、跳过用例或按固定输入写死；并发与快照恢复场景自行构造验证；只做本地实现，不引入第三方库。

## 运行

```bash
go test ./...
```
