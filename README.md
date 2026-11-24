# 任务包 batch48-go-game-a-21

## 模块

- module: gsb/batch48-go-game-a-21
- package: task
- file: skill_cooldown_guard.go

## 题面

冷却面板改版后，技能图标的层数与战斗结算对不上。玩家连按同一技能时，图标上明明还亮着一层，按下去却提示不可用；共享冷却组里的副技能灰条长短不一，短的那个先亮起来，点下去又被拒绝；冷却缩减堆过上限之后，再换一件减冷却装备，面板数字毫无变化，可战斗日志里同一次施法的冷却终点又前后不一致。运维重启副本进程后，消耗到一半的充能层数和共享组冷却一起丢失，玩家进本第一件事就是连放两个大招；另外有人发现反复刷新技能面板会让充能恢复比预期慢半拍。落点 gsb/batch48-go-game-a-21 的 skill_cooldown_guard.go，类型 SkillCooldownGuard，签名：NewSkillCooldownGuard(Options) *SkillCooldownGuard、Define(SkillDef) error、Cast(string, int64) (CastResult, error)、Ready(string, int64) bool、Charges(string, int64) int、Reduce(string, int) error、Reset(string) error、CooldownRemaining(string, int64) int64、GroupRemaining(string, int64) int64、Skills() []string、Snapshot() ([]byte, error)、Restore([]byte) error。

要修：
一、Define 遇到空名、冷却非正、多层充能却缺恢复步长的定义，一律报 ErrInvalidSkillDef；同名重复登记报 ErrDuplicateSkill；对未登记技能施法报 ErrUnknownSkill。
二、无层可用时 Cast 报 ErrNotReady，层数、冷却锚点与共享组状态都不允许被改动。
三、施法成功扣一层，CastResult 带回技能名、发生时刻、剩余层数、层数上限、冷却终点与所属组。
四、充能按 RechargeMs 的整层步长恢复，不足一层的零头毫秒留到下次继续累计，反复查询不得把零头吃掉；到达整层边界的那一毫秒就恢复满层。
五、共享冷却组内任一技能施法后，整组按 Options.GroupCooldownMs 冷却，组内其他技能同步不可用；个体冷却比组冷却长时取两者较大值，组冷却不得反过来缩短个体冷却。
六、Reduce 按百分比缩短本次冷却，多次调用累加，累加结果截断在 0 到 Options.MaxReducePct 之间；负值扣回时按 0 兜底，不得压成负数；缩减在施法时结算并体现在冷却终点上。
七、Reset 把技能恢复满层并清掉个人冷却，随后立即可施法。
八、CooldownRemaining 与 GroupRemaining 都按毫秒查询，已到期返回 0，边界那一毫秒算已到期；Skills 返回升序的全部技能名。
九、快照要带回层数、充能锚点、缩减累计与共享组截止，恢复后能接着推进；坏快照报 ErrBadSnapshot，当前状态原样保留。
十、对未登记技能查 Ready、Charges 只返回 false 与 0，不新建状态也不 panic。
十一、同一时刻重复查询层数、剩余冷却与组剩余，结果一致，不随查询次数漂移。
十二、组冷却与个体冷却不同时到期时，Ready 必须等两者都到期才为真。

硬约束（并发与原子性）：Cast、Charges、Ready 会被多路协程同时调用，扣层、写充能锚点与写组冷却必须在同一次提交里完成，同一技能的同一层只允许成功一次，其余并发调用拿到 ErrNotReady 而不是旧层数；查询读到的必须是完整状态，不允许看见扣了层却没写冷却的中间态。层数与缩减百分比都是硬边界，任何入口都不得越过层数上限或缩减上限。

硬约束（快照与重启）：快照与写入互斥，恢复要么整套生效，要么整套不生效；恢复后的层数、充能锚点、组冷却与缩减累计都要能继续推进，不能出现层数回来了、冷却锚点却被清零的半套状态。

验收：go test ./... 15 条全过，不改测试、不跳过、不写死；并发与重启自验；仅本地实现。

## 运行方式

```bash
go test ./...
```
