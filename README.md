# 任务包 batch48-go-game-a-30

## 模块

- module: gsb/batch48-go-game-a-30
- package: task
- file: loot_table_roller.go

## 题面

掉落系统改版后，玩家开始集体投诉。同一场战斗的战利品在结算面板和背包里不一样，重连之后又变成第三份；固定队连刷十几次，保底装备一次没出，反过来有人第一次就出两件。数量也乱：标称一到三个的矿石，偶尔掉出五个；幸运堆得很高之后，本该只影响稀有组的加成，普通材料的数量也跟着涨。批次结算更严重：同一批里重复提交同一个请求键，会重复扣体力、重复发奖，客服只能手动回滚。落点 gsb/batch48-go-game-a-30 的 loot_table_roller.go，类型 LootTableRoller，签名：NewLootTableRoller(Options) *LootTableRoller、LoadTable(id string, entries []Entry) error、Roll(req RollRequest) (Drop, error)、Batch(reqs []RollRequest) ([]Drop, error)、Pity(player, table string) (int, error)、ResetPity(player, table string) error、EffectiveWeights(table string, luck int) (map[string]int, error)、Table(id string) []Entry、Tables() []string、Snapshot() ([]byte, error)、Restore([]byte) error。

要修：
一、LoadTable 遇到空 id 报 ErrInvalidTable，空条目报 ErrEmptyTable；条目 ID 为空、权重为负、数量下界小于 1、上界小于下界、全表权重为零，报 ErrInvalidEntry；同表重复条目 ID 报 ErrDuplicateEntry。
二、Roll 的空请求键或空玩家报 ErrInvalidRequest，未装载的表报 ErrUnknownTable；请求键参与随机流派生，同一键必须得到同一份结果。
三、权重抽取只用整数比例，装载时归一，多次抽取不得出现小数权重累积误差；权重为零的普通条目永远不会被抽中。每次 Roll 只抽一个条目、产出一件物品，保底命中时用保底条目替换本次抽取。
四、数量在下界与上界之间取值，并按条目的 HardMax 兜底；上下界相同时按定值给，幸运加成不得把数量推过表里声明的硬上限。
五、幸运只作用于 Options.LuckGroup 指定的分组；EffectiveWeights 返回加成后的整数权重，组外条目保持原值，负幸运按零处理。
六、保底条目在连续 PityLimit 次未出后强制出现；保底触发后计数要清零而不是减一，保底条目本身仍留在正常抽取池里。
七、Pity 按玩家与表分别记账，未出保底条目才累加；ResetPity 把计数清零，未登记的表报 ErrUnknownTable，未出过手的玩家返回 0；表里没有保底条目时计数照常累计但永不触发。
八、Batch 按请求顺序返回同样长度的结果，每条结果的 Key 与请求一致；任一请求非法时整批报错，不做半批发放。
九、同一批次里重复的请求键只能产生一次掉落，重复方拿到与首次相同的结果，而不是重新抽。
十、Roll 与 Batch 共享同一套保底记账；单次 Roll 与批量中的同一玩家累计口径一致，不得一个记一次、另一个记两次。
十一、Snapshot 覆盖已装载的表、保底计数与选项；Restore 遇坏数据报 ErrBadSnapshot 且原状态保留，恢复后保底计数与表定义继续可用。
十二、并发 Roll 同一玩家时必须原子记账，保底计数不得丢加或重复加，快照与写入互斥。

硬约束（并发与原子性）：同一玩家同一张表的保底计数是硬边界，并发 Roll 与 Batch 不得让它出现负值或跳号；表在装载后只读，抽取期间不得被半更新。

硬约束（快照与重启）：快照与写入互斥，恢复失败时保底计数与表定义原样保留；重启后同一请求键必须仍能复现同一份掉落，保底计数不得回退到旧值。

验收：go test ./... 15 条全过，不改测试、不跳过、不按固定输入写死；并发与重启自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```
