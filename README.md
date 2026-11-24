# batch48-go-game-a-34

- 模块路径：`gsb/batch48-go-game-a-34`
- 包名：`task`
- 实现文件：`bag_slot_allocator.go`
- 测试目录：`tests/`

## 题面

背包系统上线后玩家反馈：捡第三件装备就提示背包已满，可背包下半部分还整排空着；整理以后同样大小的物品顺序来回跳，每次整理位置都不一样；两名玩家同时点拾取，同一个格子被塞进两件东西。请修 gsb/batch48-go-game-a-34 模块里的 bag_slot_allocator.go，类型名为 BagSlotAllocator，包名 task，只用标准库。

对外接口按下列签名实现：NewBagSlotAllocator(Options) *BagSlotAllocator、Register(player string, w, h int) error、Insert(player string, item Item) (Placement, error)、Remove(player, itemID string) error、Find(player string, w, h int) (Placement, bool)、Compact(player string) (int, error)、Move(player, itemID string, at Placement) error、Usage(player string) (int, int)、Snapshot() ([]byte, error)、Restore([]byte) error，以及 At、ItemPlacement、Items。Placement 记录 X、Y、W、H、Rotated；Item 记录 ID、W、H、Rotatable；Options 含 DefaultW、DefaultH、AllowRotate。

十二条行为约束：

一、Register 重复玩家返回 ErrBagExists，宽或高非正返回 ErrBadPlacement；未注册玩家的任何操作返回 ErrNoSuchBag；Usage 返回已占格数与总格数。

二、Insert 固定行优先扫描：先按 y 递增再按 x 递增，取左上角第一个放得下的矩形；成功返回 Placement，X、Y 为左上角，W、H 为物品尺寸，Rotated 为 false。

三、物品宽高超过网格任一方向可容纳的尺寸返回 ErrItemTooLarge；网格有空格但凑不出连续矩形返回 ErrNoSpace。

四、同一玩家内物品 ID 唯一，重复返回 ErrDuplicateItem；Insert 任一步失败都要把已占格释放干净，Usage 不变。

五、Find 只报告位置不占格，按精确 w 乘 h 与行优先顺序查找首个可行矩形，返回 Placement 与 true，Placement 的 W、H 等于查询尺寸，找不到返回 false；同参数连续调用两次结果相同。

六、Remove 按 ID 释放占用，未知 ID 返回 ErrNoSuchItem；释放出的格子可被后续 Insert 使用，且仍按行优先取最小位置。

七、Move 只读取目标 Placement 的 X、Y，宽高取物品当前尺寸；越界返回 ErrBadPlacement，与其它物品重叠返回 ErrSlotOccupied 且原位置不变，成功时旧格释放新格占用，Usage 不变。

八、At 返回该格物品 ID，空格返回 false；ItemPlacement 返回物品当前矩形；Items 按 ID 升序返回副本。

九、Compact 做无洞压缩：先按占用面积降序、同面积按原 Y 升序再按原 X 升序排序，依次行优先重放，返回位置发生变化的物品数；整理后 Usage 与物品集合不变。

十、整理后同面积物品的相对次序必须稳定，连续整理两次第二次返回 0，不能每次整理都换位置。

十一、只有 Options.AllowRotate 为真且物品 Rotatable 为真才允许旋转；旋转后按交换后的宽高重新校验边界与占用，Placement.Rotated 置 true；不允许旋转时按原地放不下处理。

十二、并发 Insert 争抢最后一个可放位置时只能成功一个，另一个返回 ErrNoSpace，失败方不得留下已占位但未入库的记录。

硬约束：每个玩家一把锁，Insert 必须先占位再登记，失败路径整体回滚；Snapshot 要带每个玩家的网格尺寸、物品表与格位占用位图，Restore 到空 Options 新建的实例后 Usage 与物品位置一致，坏字节返回 ErrBadSnapshot；单次整理不得改变总占用格数。

验收方式：go test ./... 的 15 条用例必须全部通过；不得修改测试、跳过用例或按固定输入写死；并发与快照恢复场景自行验证；只做本地实现，不引入第三方库。

## 运行

```bash
go test ./...
```
