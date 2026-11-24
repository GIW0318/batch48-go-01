# batch48-go-safety-c-14

- 模块路径：`gsb/batch48-go-safety-c-14`
- 包名：`task`
- 实现文件：`patch_presence_checker.go`
- 测试目录：`tests/`

## 运行方式

```bash
go test ./...
```

## 题面

漏洞通报里给了一个组件和修复版本，资产台账上同一个组件有十几个构建，运维挨个核对版本号，发现低于修复版本的构建里有两个其实已经由发行版回移了补丁，按版本一刀切会误判成未修复；反过来，有个高版本构建是内部分支自己编的，关键函数被优化掉，按版本判又说已修复。上一轮核查就因为这个结论反了，隔离了一套本不需要隔离的网关，运维要求判定必须能说清依据。

请修 gsb/batch48-go-safety-c-14 模块里的 patch_presence_checker.go，类型名为 PatchPresenceChecker，包名 task，只用标准库。接口签名保持现状：NewPatchPresenceChecker(CheckerOptions) *PatchPresenceChecker、Register(Fix) error、Check(ArtifactRef) (Presence, error)、Explain(string) (Explanation, error)、Batch([]ArtifactRef) (map[string]Presence, error)、Fingerprint([]string) string、CompareVersions(string, string) (int, error)、Fixes() []Fix、Cached(string) (Presence, bool)、Stats() CheckerStats、Snapshot() ([]byte, error)、Restore([]byte) error、Reset() error。Fix 含 ID、Component、IntroducedIn、FixedIn、Function、Digest；Function 含 Name、Instructions；ArtifactRef 含 ID、Component、Version、Functions；Presence 含 Status、Confidence、Evidence；Explanation 含 FixID、Steps；CheckerOptions 含 MaxBatch、CacheSize；CheckerStats 含 Registered、Checked、CacheHits、Uncertain。

需要修的行为：

一、CompareVersions 按点分数字段逐段比较，长度不足补零，非数字段或空版本返回 ErrInvalidVersion。

二、Register 校验 ID、组件、版本区间与摘要：ID 或组件为空、区间无法解析、IntroducedIn 不小于 FixedIn、摘要不是 64 位十六进制返回 ErrInvalidFix；同 ID 重复注册返回 ErrDuplicateFix。

三、Fingerprint 先规范化指令：去首尾空白、转小写、合并连续空格、把以 sym: 开头的符号引用统一替换为 sym:*、丢弃 nop，再按顺序拼接取 SHA-256，函数名不参与。

四、符号重命名、大小写与多余空白不改变指纹，助记符或操作数变化必须改变指纹。

五、版本低于 IntroducedIn 判定为 not_affected；版本不低于 FixedIn 且指纹吻合判定为 fixed，置信度一百，证据同时包含 version 与 fingerprint。

六、版本落在受影响区间且指纹不吻合判定为 vulnerable。

七、版本落在受影响区间但指纹吻合判定为 fixed，属于回移补丁，置信度七十，证据只含 fingerprint。

八、版本不低于 FixedIn 但指纹不吻合判定为 uncertain，两条证据冲突时不猜，Uncertain 计数加一。

九、Check 按组件找修复，组件没有注册过返回 ErrUnknownFix；同一产物重复 Check 命中缓存只算一次校验。

十、Explain 返回该产物最近一次判定的修复 ID 与证据步骤，步骤顺序固定为 version、fingerprint、结论，未判定过的产物返回 ErrUnknownArtifact。

十一、Batch 按产物 ID 去重后逐个判定，返回以 ID 为键的映射，重复 ID 只算一次并计入 CacheHits；超过 MaxBatch 返回 ErrBatchTooLarge。

十二、Fixes 按 ID 升序返回副本，Stats 统计注册、校验、缓存命中与不确定次数。

硬性约束：同一产物被多个协程并发判定时只真正计算一次，其余从缓存取，结果完全一致；CacheSize 封顶，超出按最久未用淘汰。快照与恢复要保住注册表、缓存与统计，重启后 Cached 与 Explain 仍可回答，坏快照返回 ErrBadSnapshot 且不破坏内存状态。

验收：go test ./... 的 15 条用例全过，不改测试、不跳过、不按固定输入写死；并发判定与重启恢复自行验证；只做本地实现。
