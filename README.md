# batch48-go-game-a-33

- 模块路径：`gsb/batch48-go-game-a-33`
- 包名：`task`
- 实现文件：`equipment_slot_guard.go`
- 测试目录：`tests/`

## 题面

换装系统改版后玩家反馈：替换一件护甲时另一件装备被顶掉，槽位计算全乱；双手剑从主手卸下后副手还挂着旧的双手模型，属性也还在加；套装件数时有时无，预览面板连续看两次数字还不一样。请修 gsb/batch48-go-game-a-33 模块里的 equipment_slot_guard.go，类型名为 EquipmentSlotGuard，包名 task，只用标准库。

对外方法不能改名换签名：NewEquipmentSlotGuard(Options) *EquipmentSlotGuard、Register(charID string, c Character) error、Equip(charID string, item Item, slot string) (Change, error)、Unequip(charID, slot string) (Change, error)、Stats(charID string) Stats、SetBonus(charID string) []Bonus、Preview(charID string, changes []Change) (Stats, error)、Repair(charID string) error、Snapshot() ([]byte, error)、Restore([]byte) error，以及 Slots、SlotItem。Stats 是力量、敏捷、耐力、护甲四元组；Change 记录 Slot、Old、New；Bonus 记录 SetID、Pieces、Armor。

十二条必须满足的行为：

一、Register 重复角色返回 ErrDuplicateCharacter；任何操作遇到未注册角色返回 ErrUnknownCharacter；Stats 返回 Base 加上所有已装备物品属性之和；Slots 返回当前槽位到物品的副本，新角色为空。

二、槽位表来自 Options.Slots，不在表内的槽位返回 ErrUnknownSlot；Equip 成功返回 Change，Old 为被替换物品，首次装备 Old 为零值，New 为本次物品。

三、物品 Level 高于角色 Level 返回 ErrLevelTooLow；物品 Class 非空且与角色职业不同返回 ErrWrongClass。

四、同一槽位替换时先扣旧物品属性再加新物品属性，只保留一件；同一物品 ID 不能重复装备，双手武器在 main 与 off 的同一份引用除外，其余情况返回 ErrDuplicateItem。

五、Unequip 空槽返回 ErrSlotEmpty，成功返回 Change 且 Old 为原物品、New 为零值；卸下后属性回到不含该物品的值。

六、TwoHanded 物品只能装进 main；装备成功后 off 槽同时指向同一件物品，但属性只计一次；off 原有的普通物品被替换掉。

七、main 持有双手武器时向 off 装备任何物品返回 ErrOffhandBlocked。

八、双手武器被单手武器替换或从 main 卸下时，off 槽必须同时释放，并按释放后的装备重新统计套装件数，不能只改主手槽。

九、套装按 SetID 统计件数，达到 2 件产出 Bonus，Pieces 为件数、Armor 为 10 乘件数；不足 2 件不产出；换装或卸装后立即重算。

十、Preview 接收 Change 列表，New.ID 非空表示装备、为空表示卸下；任一条非法就返回对应错误；同一组变更连续调用两次返回逐位相同的结果，且不改变 Stats 与槽位。

十一、一次 Equip 或 Unequip 要么整体生效要么完全不生效：等级、职业、槽位任一校验失败时不得先卸后装，不得留下空槽或半更新的属性。

十二、Repair 按当前槽位重算属性，重复调用结果不变，也不改动槽位内容；未知角色返回 ErrUnknownCharacter。

硬约束：同一角色、同一槽位的换装由单把锁串行，属性增量与套装件数必须同一次提交，不能出现属性已加而槽位未换的中间态；Snapshot 要覆盖 Options、角色基础值与全部槽位，Restore 到空 Options 新建的实例后属性与套装一致，坏字节返回 ErrBadSnapshot。

自测要求：go test ./... 的 15 条用例必须全部通过；不得修改测试、跳过用例或按固定输入写死；并发与快照恢复场景自行验证；只做本地实现，不引入第三方库。

## 运行

```bash
go test ./...
```
