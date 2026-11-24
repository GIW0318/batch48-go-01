# 任务包 batch48-go-game-a-23

## 模块

- module: gsb/batch48-go-game-a-23
- package: task
- file: threat_table_guard.go

## 题面

坦克的仇恨面板与怪物实际目标对不上。嘲讽一结束，怪立刻黏回输出职业，坦克再打几下也拉不回来；两个输出职业的仇恨咬得很近时，怪的目标在两人之间来回跳，同一段战斗日志重放两次，锁定对象居然不同；大治疗刷下去之后，治疗的仇恨反而比坦克还高，治疗被迫停手。还有更隐蔽的：有人把仇恨转移给队友后，原主人的仇恨没有按比例扣掉，转移前后总量对不上；怪物跑出警戒范围本该自然衰减，面板上的数字却一直挂着不动。落点 gsb/batch48-go-game-a-23 的 threat_table_guard.go，类型 ThreatTableGuard，签名：NewThreatTableGuard(Options) *ThreatTableGuard、AddDamage(string, string, int64) error、AddHeal(string, string, int64) error、Taunt(string, string, int64) error、Current(string, int64) (string, bool)、Transfer(string, string, int) error、Tick(int64)、Table(string) []Entry、SetInRange(string, string, bool) error、SetVisible(string, string, bool) error、Snapshot() ([]byte, error)、Restore([]byte) error。

要修：
一、伤害或治疗量为零或负数报 ErrInvalidAmount；目标名、玩家名为空报 ErrInvalidRequest。
二、伤害按 Options.DamageFactorPct 折算后累加到该玩家的仇恨，多次追加等于各次增量之和。
三、治疗按 HealCasterPct 与 HealTargetPct 分别记给施法者和当前目标；尚未锁定目标时，两份都记给仇恨最高者；两个比例之和超过一百时按比例归一，不得两份都记全额。
四、嘲讽在 until 之前强制锁定该玩家，窗口内不因仇恨高低换目标；until 非正报 ErrInvalidTaunt；嘲讽到期后立即回到按仇恨排序的结果，不能停在嘲讽者身上。
五、Current 取仇恨最高者；挑战者要超过当前目标的 SwitchRatioPct 并且持续 StickMs 毫秒才完成切换，迟滞窗口内保持原目标。
六、仇恨同分时按玩家名升序取第一个，结果不随 map 遍历或插入顺序摇摆。
七、Transfer 按 pct 把来源玩家的仇恨转给目标玩家，pct 不在 1 到 100 报 ErrInvalidTransfer，来源不在表报 ErrUnknownPlayer；目标不在表时补一行，转移前后总量守恒。
八、Table 返回按仇恨降序、同分按玩家名升序的副本，调用方改动返回值不得影响内部表。
九、Tick 把超距或不可见的条目按 DecayPct 衰减，衰减后可以到零但条目保留；在距且可见的条目不衰减。
十、快照恢复各目标的仇恨表、当前锁定、迟滞候选与嘲讽窗口，恢复后 Current 结果与恢复前一致。
十一、坏快照报 ErrBadSnapshot，现有仇恨表原样保留。
十二、未登记目标的 Current 返回空与 false，Table 返回空切片，不 panic。

硬约束（并发与原子性）：Tick 与伤害、治疗、嘲讽、转移并发执行时，读到的必须是整张表，不得看见只更新了一半的行；所有写入串行合并，并发追加后的总仇恨严格等于各次增量之和，任何一条写入都不会因为竞争丢失。

硬约束（快照与重启）：快照与写入互斥，恢复要么整套生效要么整套不生效；重启后仇恨、锁定与迟滞计时都要能继续推进，不能出现仇恨回来了但锁定丢失、或者嘲讽窗口被清零的情况。

验收：go test ./... 15 条全过，不改测试、不跳过、不写死；并发与重启自验；仅本地实现。

## 运行方式

```bash
go test ./...
```
