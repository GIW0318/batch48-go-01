# batch48-go-safety-c-11

- 模块路径：`gsb/batch48-go-safety-c-11`
- 包名：`task`
- 实现文件：`rule_bundle_swapper.go`
- 测试目录：`tests/`

## 运行方式

```bash
go test ./...
```

## 题面

值班同学凌晨升级规则包之后，线上扫描进程的命中忽新忽旧：同一个样本在两条流水线上被不同版本的规则判成两种结果，工单里附的命中编号在回滚之后查不到来源，只能凭日志猜是哪一版规则打的。规则包是整包下发的，运维希望替换动作对外是一个瞬间：要么整份旧包，要么整份新包，不能出现新旧规则混在同一份生效包里的情况。

请修 gsb/batch48-go-safety-c-11 模块里的 rule_bundle_swapper.go，类型名为 RuleBundleSwapper，包名 task，只用标准库。接口签名保持现状：NewRuleBundleSwapper(SwapperOptions) *RuleBundleSwapper、Stage(*RulePack) (uint64, error)、Publish(uint64) error、Rollback(uint64) error、Active() *RulePack、Evaluate(string) ([]Hit, error)、ResolveHit(string) (uint64, error)、Retained() []uint64、Pack(uint64) (*RulePack, bool)、Digest(*RulePack) string、Stats() SwapperStats、Snapshot() ([]byte, error)、Restore([]byte) error、Reset() error。RulePack 含 Version、Name、Rules；Rule 含 ID、Pattern、Severity；Hit 含 ID、RuleID、Version、Sample；SwapperOptions 含 MaxStaged、MaxRetained；SwapperStats 含 Staged、Published、Rollbacks、Hits、Evicted。

需要修的行为：

一、Stage 拒绝空包和内容非法的包：版本为零、名称为空、规则为空、规则 ID 为空或重复、Severity 不在 1 到 5、Pattern 为空都返回 ErrInvalidPack，入参为空指针返回 ErrNilPack。

二、同一版本号重复进入暂存区，内容一致时幂等返回原版本且暂存计数不变；内容不一致返回 ErrVersionConflict 并保持原包不动。

三、暂存区数量超过 MaxStaged 返回 ErrStageLimit，已有包不受影响。

四、Publish 只接受仍在暂存区的版本，否则返回 ErrNotStaged；同一版本重复发布幂等，发布计数只加一次。

五、发布是整包原子切换：Active 返回的包结构完整，读方看到的要么是旧包要么是新包；从未发布时 Active 返回 nil。

六、Evaluate 只使用当前生效包，按规则顺序扫描样本，每条命中记录规则 ID 和产生它的包版本；没有生效包时返回 ErrUnknownBundle。

七、ResolveHit 按命中编号返回当初的包版本，回滚之后既有命中仍按记录版本解析，未知编号返回 ErrHitNotFound。

八、Rollback 只接受已发布且仍在保留集合里的版本，未发布过的版本返回 ErrUnknownBundle；回滚到当前生效版本视为幂等，不重复计数；回滚到已淘汰版本返回 ErrBundleEvicted。

九、保留集合最多 MaxRetained 个版本，超出时淘汰最早已下线的版本，Retained 升序返回，Evicted 计数加一；当前生效版本不得被淘汰。

十、Digest 覆盖规则内容且与规则顺序无关，Severity 或 Pattern 变化必须改变摘要。

十一、Stats 分别统计暂存、发布、回滚、命中、淘汰次数，Reset 之后全部归零。

硬性约束：Publish、Rollback 与 Evaluate、Active 并发时，单次扫描只绑定一个版本，命中编号不会同时指向两个包；同一版本的并发发布只能产生一次切换。Snapshot 与 Restore 要保住生效版本、保留集合、命中映射和统计，重启后 ResolveHit 结果与中断前逐条一致，坏快照返回 ErrBadSnapshot 且不破坏现有状态。

验收：go test ./... 的 15 条用例全过，不改测试、不跳过、不按固定输入写死；并发发布与重启恢复自行验证；只做本地实现。
