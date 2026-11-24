# batch48-go-game-a-35

- 模块路径：`gsb/batch48-go-game-a-35`
- 包名：`task`
- 实现文件：`duel_round_guard.go`
- 测试目录：`tests/`

## 题面

决斗场新赛季出现三类事故：先攻相同的两名玩家每局先后手都不一样；有人在回合外连点技能，技能进了冷却但行动点没扣；超时自动行动之后，中毒玩家这一回合的持续伤害没有结算。请修 gsb/batch48-go-game-a-35 模块里的 duel_round_guard.go，类型名为 DuelRoundGuard，包名 task，只用标准库。

接口签名保持现状：NewDuelRoundGuard(Options) *DuelRoundGuard、Join(player string, init int) error、Begin(at int64) (Turn, error)、Act(player string, action Action) (Turn, error)、Timeout(at int64) (Action, error)、Surrender(player string) error、Disconnect(player string) error、Result() (Result, bool)、Snapshot() ([]byte, error)、Restore([]byte) error，以及 TurnOrder、ActionPoints、HP、Round。Turn 含 Round、Player、ActionsLeft；Action 含 Kind、Target、Power、Cost、Cooldown；Result 含 Winner、Loser、Reason、Rounds。

十二条行为规则：

一、Join 最多两人，成功后 HP 等于 Options.MaxHP；第三人返回 ErrDuelFull，重复加入返回 ErrAlreadyJoined，Begin 之后加入返回 ErrAlreadyStarted；不足两人调用 Begin 返回 ErrNotEnoughPlayers，未 Begin 就行动返回 ErrNotStarted。

二、Begin 按先攻值降序排行动顺序，先攻相同按加入顺序稳定排序，之后有人退出也不得重排剩余顺序；Begin 之前 Round 为 0，开始后从 1 计；当前玩家 ActionsLeft 等于 Options.ActionPoints，TurnOrder 返回该顺序的副本。

三、Act 只接受当前玩家，否则返回 ErrOutOfTurn 且不扣行动点、不产生伤害；未知玩家返回 ErrUnknownPlayer；终局后返回 ErrDuelOver；同一回合内重复提交同一动作只生效一次。

四、Action.Cost 小于 1 时按 1 计；扣完仍有剩余行动点就继续由同一玩家行动，归零则轮到下一名存活玩家并跳过已退出者，绕回首位时 Round 加一；ActionPoints 返回该玩家当前剩余行动点，非当前玩家为 0。

五、attack 对 Target 造成 Power 点伤害，Target 为空时打对手，HP 不低于 0；defend 与 wait 不造成伤害。

六、skill 造成 Power 点伤害并把该玩家冷却设为 Action.Cooldown；冷却在玩家每个回合开始时减 1，大于 0 时再次提交 skill 返回 ErrOnCooldown 且不消耗行动点、不产生伤害。

七、行动点不够支付 Cost 时返回 ErrNotEnoughActions，本次行动的所有副作用都不发生。

八、HP 归零即终局，胜者为对手、Reason 为 ko、Rounds 为当前回合；Result 终局前返回 false，终局后重复调用返回同一份结果。

九、Surrender 让对手以 surrender 获胜，Disconnect 以 disconnect 结算，两种原因不得混用；终局后任何 Act 与 Timeout 都返回 ErrDuelOver。

十、Timeout 用 Options.AutoAction 替当前玩家执行一次行动，AutoAction 为零值时按 wait 处理，返回实际执行的 Action，并走与手动行动相同的回合推进路径。

十一、中毒等持续伤害在每名玩家回合开始时结算，先于该回合的行动与超时自动行动；扣血导致 HP 归零同样判负，结算顺序必须与手动行动一致。

十二、每名玩家的行动点与冷却互相独立记账，一个玩家的冷却不因对手行动而变化；行动点在回合结束后回满，不跨回合累计。

硬约束：回合推进由单把锁串行，Act 与 Timeout 并发时只有一方推进同一回合，另一方拿到 ErrOutOfTurn 或看到推进后的状态；Snapshot 要覆盖 Options、玩家、先攻顺序、HP、行动点、冷却与终局结果，Restore 到空 Options 新建的实例后状态一致，坏字节返回 ErrBadSnapshot。

验收标准：go test ./... 的 15 条用例必须全部通过；不得修改测试、跳过用例或按固定输入写死；并发与快照恢复场景自行验证；只做本地实现，不引入第三方库。

## 运行

```bash
go test ./...
```
