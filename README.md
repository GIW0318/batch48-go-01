# batch48-go-game-c-11

## 题面

转码边缘节点上线 SVC 后，多分辨率直播的用户侧开始连续报障：同一路流里，低带宽用户被推到了最高时域层，卡顿黑屏；另一些用户中途降档后画面冻结十几秒才恢复；运维在扩缩容时重启了转发进程，回来后一部分订阅者明明在线，却一直收不到此前已经在转发的层，只能等客户端重连。还有按流量计费的房间，预算下调后仍在按原层转发，账单直接翻倍。请修 gsb/batch48-go-game-c-11 模块里的 svc_layer_router.go，类型 LayerRouter，包名 task，只允许使用标准库。

接口保持：NewLayerRouter(RouterConfig) *LayerRouter、Subscribe(id string, budget int) error、SelectLayers(id string, want LayerID) (LayerID, error)、OnKeyframe(id string, at uint64) (LayerID, error)、SetBudget(id string, budget int) (LayerID, error)、Detach(id string) error、Forwarded(id string) LayerID、Pending(id string) (LayerID, bool)、Coverage(want LayerID) []LayerID、DropPlan(from, to LayerID) []LayerID、Cost(want LayerID) int、ActiveLayers() []LayerID、Snapshot() ([]byte, error)、Restore([]byte) error、Stats() RouterStats。LayerID 含 Temporal、Spatial、Quality 三个从 0 起的整数，0 为最低层；RouterConfig 含 MaxSubscribers、MaxLayers；RouterStats 含 Subscribers、Layers、Upgrades、Downgrades、Keyframes、BudgetRejects、Detached、Restored。

需要修的行为：

一、订阅入口：首尾空白视为空，返回 ErrBlankSubscriber；budget 非正返回 ErrBudgetInvalid；在册订阅者达到 MaxSubscribers 返回 ErrSubscriberLimit；Detach 之后名额立刻释放。

二、Coverage 按依赖闭包返回所有分量都不超过目标的层，按 Temporal、Spatial、Quality 升序排列；Cost 等于闭包层数乘 LayerUnitCost。

三、订阅者初始转发 0/0/0。任一维变大且没有维变小的选择只登记为待切换目标，要等 OnKeyframe 才生效，Upgrades 与 Keyframes 同时加一，Pending 报告目标。

四、任一维变小且没有维变大的选择立即生效并清掉待切换目标，Downgrades 加一；DropPlan 列出被移除的层，依赖方排在依赖之前。

五、既有维度变大又有维度变小的选择返回 ErrMixedSwitch，当前层与待切换目标都不许改动。

六、目标层闭包的成本超过订阅者预算时返回 ErrBudgetExceeded，BudgetRejects 加一，且不登记待切换目标。

七、SetBudget 下调到无法维持当前层时，直接在预算内挑依赖序最高的层生效并计 Downgrades；连 0/0/0 都盖不住时返回 ErrBudgetExceeded 并保留原预算。

八、多个订阅者共用同一层时该层只占一份，全部引用释放后才从 ActiveLayers 消失；活跃层总数逼近 MaxLayers 时，会突破上限的选择返回 ErrLayerLimit，原选择保持不变。

九、时域层降级后恢复必须从被丢帧之后的第一个可解码帧开始，直接跳到当前帧会造成花屏，恢复点由 router 自己记录。

十、引用计数归零的层在快照恢复后若仍有订阅者引用必须重新激活，不能按已移除处理。

十一、预算上调后升层仍要等关键帧，不能因为预算宽裕就立刻切到高层。

十二、快照保存每个订阅者的预算、当前层、待切换目标与全部计数；坏数据 Restore 返回 ErrRouterSnapshot 且不改动现网状态。

并发与硬约束：同一订阅者的选择、关键帧与解绑必须串行，快照与选择互斥，任何快照只能看到完整的旧状态或完整的新状态；层数与订阅者数都是硬顶，超限拒绝而不是静默扩容；关键帧时间只用于记录切换点，不参与预算判断。

验收：go test ./... 的 15 条用例全过，不得改测试、跳过或按固定输入写死；并发与重启恢复自行验证；只做本地实现。

## 模块与运行

- 模块路径：`gsb/batch48-go-game-c-11`
- 包名：`task`
- 实现文件：`svc_layer_router.go`
- 测试目录：`tests/`

```bash
go test ./...
```
