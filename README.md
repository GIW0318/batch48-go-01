# batch48-go-safety-c-15

- 模块路径：`gsb/batch48-go-safety-c-15`
- 包名：`task`
- 实现文件：`scan_coverage_ledger.go`
- 测试目录：`tests/`

## 运行方式

```bash
go test ./...
```

## 题面

季度合规检查要出一张覆盖账：每个资产上有哪些规则真正扫过、哪些还是缺口。上一轮平台把规则包从 v3 升到 v4，看板上一切照旧全绿，手工抽查却发现新规则一条都没跑过；另有一批资产换过扫描器版本之后，旧扫描器留下的覆盖记录还在，缺口列表里凭空少了几十条。审计组要求每次缺口都能说清是从来没扫过，还是版本变了记录作废。

请修 gsb/batch48-go-safety-c-15 模块里的 scan_coverage_ledger.go，类型名为 ScanCoverageLedger，包名 task，只用标准库。接口签名保持现状：NewScanCoverageLedger(LedgerOptions) *ScanCoverageLedger、RegisterAsset(Asset) error、MarkRun(Run) error、Gaps(Query) ([]Gap, error)、Invalidate(string) error、Coverage(string) []Coverage、Assets() []string、SetRuleVersion(int) error、RuleVersion() int、SetScannerVersion(int) error、ScannerVersion() int、ShardOf(string) int、Stats() LedgerStats、Snapshot() ([]byte, error)、Restore([]byte) error、Reset() error。Asset 含 ID、Rules、Priority；Coverage 含 AssetID、RuleID、RuleVersion、ScannerID、ScannerVersion、At；Run 含 AssetID、Rules、RuleVersion、ScannerID、ScannerVersion、At；Query 含 Assets、MinPriority、Limit；Gap 含 AssetID、RuleID、Priority、Reason；LedgerOptions 含 MaxAssets、MaxGaps、Shards、ScannerVersion；LedgerStats 含 Assets、Covered、Gaps、Runs、RuleVersion、ScannerVersion。

需要修的行为：

一、RegisterAsset 要求 ID 非空、至少一条规则、优先级在一到五之间；规则去重后升序保存；重复 ID 返回 ErrInvalidAsset，超过 MaxAssets 返回 ErrAssetLimit。

二、规则版本与扫描器版本初始都为一，配置为零按一处理；SetRuleVersion 与 SetScannerVersion 只接受正整数，切换后与当前版本不一致的旧覆盖一律失效并进入缺口。

三、MarkRun 要求资产已注册，未注册返回 ErrUnknownAsset；扫描器 ID 为空、扫描时刻不为正、规则列表为空返回 ErrInvalidRun。

四、MarkRun 的规则版本必须等于当前规则版本，扫描器版本必须等于当前扫描器版本，不等分别返回 ErrStaleRuleVersion 与 ErrStaleScannerVersion。

五、运行里出现资产规则集之外的规则返回 ErrUnknownRule；同一次运行里的重复规则去重，不重复记账。

六、覆盖按资产、规则、规则版本、扫描器版本记账，成功一次对应一条记录，At 取本次运行时刻；Coverage 按规则 ID 升序返回。

七、同一资产重复扫描只刷新对应记录的时刻与扫描器信息，Covered 不重复累加且只反映当前有效的组合数，Runs 照常增加。

八、Gaps 计算当前两个版本下缺少有效覆盖的资产与规则组合；从来没有记录的缺口原因记 missing，有记录但版本已经失效的记 stale。

九、Gaps 的排序固定为优先级降序、资产 ID 升序、规则 ID 升序，优先级低的资产不会插到高优先级前面。

十、Query 的 Assets 非空时只算这些资产，未知资产返回 ErrUnknownAsset；MinPriority 过滤低优先级；Limit 为零时用 MaxGaps，超过上限只返回前 Limit 条，Stats 的缺口总数按全量计数。

十一、Invalidate 删除该资产全部覆盖并让组合重新进入缺口，未知资产返回 ErrUnknownAsset；ShardOf 由资产 ID 稳定散列取模，Shards 小于一时按单片处理。

十二、Assets 升序返回已注册资产，Stats 汇总资产数、已覆盖组合数、缺口总数、运行次数与两个当前版本；Reset 清空资产、覆盖与统计并把版本复位为一。

硬性约束：MarkRun 与 Gaps 并发时，缺口计算看到的是完整的一轮覆盖，不会读到写了一半的记录；同一资产被多个协程并发 MarkRun 时不产生重复覆盖，也不把已覆盖的组合算回缺口。快照与恢复后资产、两个当前版本、覆盖记录与统计逐条一致，缺口集合与重启前相同，坏快照返回 ErrBadSnapshot 且不破坏内存状态。

验收：go test ./... 的 15 条用例全过，不改测试、不跳过、不按固定输入写死；并发扫描与重启恢复自行验证；只做本地实现。
