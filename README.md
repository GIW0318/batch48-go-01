# 遮挡查询置信度代理：加权采样配额、区间判定与保守可见

## 题面

阴影视图夜里成片漏光，反射视图又把整面墙当成透明的，主相机的遮挡结论被原样套给了两个副消费者；采样预算一紧，主相机一帧能拿走上百次查询，阴影与反射各自只剩几十次，靠后的提交被无声拒掉，远处物件就开始一帧亮一帧暗。落点 gsb/batch48-go-game-b-08 的 visibility_confidence_broker.go，类型 VisibilityConfidenceBroker，签名保持：NewVisibilityConfidenceBroker(cfg Config) *VisibilityConfidenceBroker、BeginFrame(frame uint64) error、Frame() uint64、Submit(objID ObjectID, consumer Consumer, passed, total uint32) error、Visible(objID ObjectID, consumer Consumer) (Verdict, error)、SetThresholds(low, high float64) error、Thresholds() (float64, float64)、SetWeights(weights map[Consumer]uint32) error、QuotaFor(consumer Consumer) uint32、SamplesLeft(consumer Consumer) uint32、ConflictCount() int、Invalidate(objID ObjectID)、Snapshot() ([]byte, error)、Restore(payload []byte) error。Config 带 SampleQuota、TTLFrames、Low、High 与 Weights，消费者只有 main、shadow、reflection 三个，结论为 visible、hidden、unknown；每帧每消费者的配额是 SampleQuota 乘自身权重再除以权重总和向下取整，配额按消费者独立计数，三个消费者的权重都必须为正，缺项或零权重直接报错。采样结果按帧计数保留，超过 TTL 自动回到未知态；每个消费者各自维护结论，任何一方判可见就按可见处理并记一次冲突，同一对象同一帧里两份记录都要留到查询阶段。

要修：帧号可以重复或倒退，同一帧的配额被重复发放，没开帧就提交采样也照收；未知消费者照样分配配额并接收采样；passed 大于 total 被接受，比例算出负数还当真；采样配额按消费者平均分，不按权重；配额用完后继续接收采样，旧结果被覆盖；阈值反了也生效，超出 0 到 1 的阈值被接受；比例低于低阈值判成可见、高于高阈值却判成未知；比例落在中间区间直接判可见，未知态消失；total 为 0 时直接除零，或者干脆判成不可见；两个消费者对同一对象结论冲突时后写的覆盖先写的，冲突计数恒为零；对象变换后旧采样继续生效，TTL 到期直接判不可见而不是回到未知态；快照丢掉阈值与帧号，恢复后判定退回默认阈值。

硬约束：帧号从 1 开始且只能严格递增，提交必须发生在已经开启的帧里；Submit、Visible、BeginFrame 与快照在同一把锁下串行，同一对象同一消费者在一帧里只保留最后一次提交，配额按消费者独立扣减且不得越过配置总额，配额、TTL 与冲突计数都不能为负；结论按 passed 除以 total 与两个阈值比较，低于等于低阈值判不可见、高于等于高阈值判可见、夹在中间一律未知；快照覆盖采样表、阈值、权重、TTL、帧号与每消费者本帧已用配额，恢复要么整体生效要么完全不生效，内容不完整一律拒绝并保留旧状态；过期判定用当前帧号减去采样帧号再与 TTLFrames 比较，等于 TTL 的边界只算一次；同一份采样与同一组阈值重复查询结果必须一致，不受遍历顺序影响。

验收：go test ./... 的 15 条用例全过，不得改测试、跳过或按固定输入写死；并发提交与快照恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-game-b-08`
- 实现文件：`visibility_confidence_broker.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`VisibilityConfidenceBroker`、`Config`、`Consumer`、`Verdict`、`ObjectID`
