# batch48-go-game-a-32

- 模块路径：`gsb/batch48-go-game-a-32`
- 包名：`task`
- 实现文件：`dungeon_mechanism_fsm.go`
- 测试目录：`tests/`

## 题面

副本熔炉回廊的机关维护后集体失灵：拉杆拉下后玩家一离开区域就弹回；压力板站上两个人却只按一个算；宝箱被反复开启刷出多份钥匙；同一扇门偶尔连播两次开门特效。请修 gsb/batch48-go-game-a-32 模块里的 dungeon_mechanism_fsm.go，类型名为 DungeonMechanismFSM，包名 task，只用标准库。

接口签名沿用现有定义：NewDungeonMechanismFSM(Options) *DungeonMechanismFSM、Load(spec DungeonDef) error、Activate(mech, by string, at int64) ([]Effect, error)、Deactivate(mech, by string, at int64) ([]Effect, error)、State(mech string) string、Doors() map[string]bool、Trigger(event Event, at int64) ([]Effect, error)、Reset(room string) error、Snapshot() ([]byte, error)、Restore([]byte) error，以及 DoorUnlocked、EventCount、Mechanisms。Effect 含 Kind、Target、Room、At；DungeonDef 由 Rooms、Mechanisms、Doors 组成。

十二条行为细则：

一、Load 校验：机关重名返回 ErrDuplicateMechanism；门引用定义中不存在的机关名返回 ErrUnknownMechanism；Mode 不是 all、any、count 之一返回 ErrBadMode；未 Load 时调用其它方法返回 ErrNotLoaded。

二、初始机关 State 为 idle，Doors 返回全部门名到解锁状态的副本且初始全为 false，改外部 map 不影响内部；Mechanisms 按名称升序返回副本；DoorUnlocked 对未知门返回 false。

三、Activate 遇到未知机关返回 ErrUnknownMechanism；只有状态真正从 idle 变 active 才产出 Kind 为 activate、Target 为机关名、Room 为所在房间、At 为传入时间的 Effect；重复 Activate 已激活机关不产出 Effect 也不报错。

四、Deactivate 把 active 拉回 idle 并产出 Kind 为 deactivate 的 Effect；对一次性机关调用返回 ErrNotToggleable；对 idle 机关不产出 Effect。

五、门条件按 Requires 判定：all 要求全部激活，any 要求任一激活，count 对每个 Requires 项统计机关 active 记 1 再加同名事件计数，总和达到 Count 即满足；门一旦解锁不再回收。

六、同一次调用里多个门同时满足时，unlock Effect 按门在 DungeonDef.Doors 中的先后顺序追加在机关 Effect 之后，Kind 为 unlock、Target 为门名；已经解锁的门不重复产出 unlock。

七、标了 Once 的机关首次 Activate 产出 Kind 为 reward、Target 为机关名的 Effect 并置为 used，第二次不产出 Effect；未标 Once 的机关不产出 reward。

八、Trigger 把 Event.Count 累加到 EventCount(Event.Kind)，Count 小于 1 按 1 计，然后按门定义顺序检查并产出 unlock；未知事件名不报错，只计数。

九、Reset(room) 校验房间存在，未知房间返回 ErrUnknownRoom，并把该房间的机关状态与门锁恢复初值；标了 Permanent 且已用过的一次性机关保持 used。

十、拉杆这类 Latched 机关激活后不会因为玩家离开区域而自动复位；只有标了 Momentary 的机关在 Trigger 收到 Kind 为 leave 且 Room 匹配时回到 idle 并产出 deactivate，两类机关不能共用同一套超时逻辑。

十一、同一扇门的解锁条件被两条路径同时满足时只解锁一次、只广播一次 unlock，重复触发不得重复产出；并发的 Activate 与 Trigger 由单把锁串行，Effect 顺序与串行执行一致。

十二、一次性机关在 Reset 后按定义区分是否恢复：标了 Permanent 的复位后仍是 used，未标的才清回 idle，不能全量清空。

硬约束：加载定义后只允许单把锁串行推进状态；Snapshot 覆盖定义、机关状态、门锁、事件计数与顺序进度，Restore 到空 Options 新建的实例后行为一致，坏字节返回 ErrBadSnapshot；单次调用的 Effect 数不超过机关数加门数。

交付验收：go test ./... 的 15 条用例必须全部通过；不得修改测试、跳过用例或按固定输入写死；并发与快照恢复场景自行验证；只做本地实现，不引入第三方库。

## 运行

```bash
go test ./...
```
