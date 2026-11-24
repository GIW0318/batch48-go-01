# batch48-go-safety-c-02

模块路径 `gsb/batch48-go-safety-c-02`，包名 `task`，实现文件 `stream_signature_scanner.go`，主类型 `StreamSignatureScanner`。

## 题目

大文件与网络流的特征扫描服务上线后问题不断：一个特征正好横跨两个读块，结果一次都没报；同一份尾部数据被上游重投一次，命中列表翻倍，值班同学以为又有两台机器中招；精确特征和带通配间隔的特征同时命中同一位置，报表里出现两条重复记录，偏移还各写各的；某次把匹配窗口调大后内存一路涨到十几 G，最后被系统杀掉；还有一次索引重建到一半，扫描线程拿着半份索引跑了十分钟，漏检了整整一个批次。

请修 `gsb/batch48-go-safety-c-02` 模块里的 `stream_signature_scanner.go`，类型名 `StreamSignatureScanner`，包名 `task`，只用标准库。

接口保持：`NewStreamSignatureScanner(Options) *StreamSignatureScanner`、`BuildAtoms([]Pattern) (*AtomIndex, error)`、`Install(*AtomIndex) error`、`Feed(chunk []byte, off int64) ([]Hit, error)`、`Flush() []Hit`、`SetWindow(n int) error`、`Window() int`、`Stats() ScanStats`、`Reset() error`，以及 `AtomIndex` 上的 `Size`、`Patterns`、`Version`、`Fingerprint`。

`Pattern` 有精确与通配两种：精确模式按 `Prefix` 全字面匹配；通配模式匹配 `Prefix`、一段 `1..Gap` 的任意字节、再跟 `Suffix`。命中 `Hit` 带模式 ID 与流的首末绝对偏移，末偏移不含尾字节。

要落到的十二条：

一、模式 ID 为空或重复返回 `ErrPatternInvalid`；精确模式前缀短于 3 字节、通配模式后缀为空、`Gap` 不在 `1..4096` 返回 `ErrAtomTooShort` 或 `ErrPatternInvalid`，错误文本带模式 ID。

二、原子取每条模式前缀的前三字节，相同前三字节共用一个桶；不同原子数超过 `MaxAtoms` 返回 `ErrTooManyAtoms`。

三、每次成功构建索引 `Version` 从 1 起递增；`Patterns` 按字典序返回，`Fingerprint` 对同一批模式恒定，不依赖 map 遍历。

四、`SetWindow` 只接受 `0..1048576`，越界返回 `ErrBadWindow`；窗口为 0 表示命中即时上报。

五、没有安装索引就 `Feed` 返回 `ErrNoIndex`；`Reset` 之后同样返回 `ErrNoIndex`。

六、`Feed` 的 `off` 必须与已消费字节连续，跳号返回 `ErrOffsetGap`；单块超过 `MaxChunk` 返回 `ErrChunkTooLarge`。

七、跨块模式在凑齐时完整上报一次，`Start` 与 `End` 都是流上的绝对偏移；已经上报的命中不得在后续块里重复出现。

八、精确模式与通配模式在同一起始偏移各命中一次时，去重键要同时包含模式 ID、起始偏移与结束偏移，两条都要保留且各报一次。

九、通配模式只在间隔长度落在 `1..Gap` 时命中，取最早结束的那次；间隔为 0 或超过 `Gap` 不算。

十、命中按 `Start`、`End`、模式 ID 升序输出；结束位置之后不足窗口字节数的命中先滞留在待定区，由后续 `Feed` 或 `Flush` 补报，重复调用 `Flush` 不得重复上报。

十一、待确认候选数超过 `MaxCandidates` 返回 `ErrCandidatesExceeded`，不得静默漏检；模式尾部跨两块且第二块正好是流末尾时，收尾必须确认并上报一次。

十二、索引构建与扫描可以并发，扫描只使用某一次完整构建的索引；同一块数据以相同偏移被重投时，命中序列必须与首次完全一致。

硬约束：`ScanStats` 的块数、字节数、命中数必须与实际投喂一致；并发下索引切换只能发生在两次 `Feed` 之间。验收：`go test ./...` 十五条必须全过，不改测试、不跳过、不按固定输入写死；并发与重复投递自行验证；只做本地实现。

## 运行

```
go test ./...
```
