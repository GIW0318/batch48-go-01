# batch48-go-safety-c-09

- 模块路径：`gsb/batch48-go-safety-c-09`
- 包名：`task`
- 实现文件：`binary_feature_extractor.go`
- 测试目录：`tests/`

## 题面

样本库里同一个加壳家族被标成了四十多个不同条目：文件只是节区顺序被重排过，或者尾部被塞了几百字节垃圾，提取器就认为它们是不同样本，运营的聚类看板一周内翻了三倍。反过来，有两个被截断的样本让提取服务直接崩溃重启，日志里只有一行数组越界；还有一个用序号导入的驱动被记成了空导入表，检索时永远差这一条。

请修 gsb/batch48-go-safety-c-09 模块里的 binary_feature_extractor.go，类型名为 BinaryFingerprinter，包名 task，只用标准库。接口签名保持现状：NewBinaryFingerprinter(ExtractorOptions) *BinaryFingerprinter、Detect([]byte) (Format, error)、Sections([]byte) ([]Section, error)、Imports([]byte) ([]Import, error)、FuzzyHash([]byte) (uint64, error)、Stable(Feature) bool、Extract([]byte) (Feature, error)、Features() []Feature、Capacity() int、Reset() error、Stats() ExtractorStats、Snapshot() ([]byte, error)、Restore([]byte) error。Section 含 Name、VirtualSize、RawSize、Offset、Truncated；Import 含 Library、Symbol、Ordinal；Feature 含 Digest、Format、Sections、Imports、Fuzzy、Seen；ExtractorOptions 含 MaxSections、MaxImports、MaxFeatures。

需要修的行为：

一、Detect 在文件前两字节为 MZ、0x3C 处的 PE 头偏移落在文件内且签名匹配时返回 pe；前四字节为 ELF 魔数时返回 elf；样本不足最小头部或头部偏移越界返回 ErrTruncated；其余返回 ErrUnknownFormat。

二、节区解析：PE 节表位于 PE 签名后四字节加二十字节文件头加 SizeOfOptionalHeader 处，每项四十字节，取名字、虚拟大小、原始大小与原始偏移；ELF64 按节头偏移、节头大小、节头数量与字符串表索引逐项读取，名字从字符串表取，跳过空名项，ELF 节区的偏移与大小取自节头。

三、节区按名字升序输出，同名按原始大小降序；PE 节名超过八字节的部分丢弃；节区声明的原始大小超过文件剩余长度时保留偏移，把大小截断到剩余字节并置 Truncated 为真，不报错也不越界读。

四、节区数量超过 MaxSections 返回 ErrSectionLimit；节表本身超出文件长度返回 ErrTruncated。

五、导入解析只支持 PE：从可选头数据目录第一项取导入表地址，按节表映射到文件偏移，逐个读二十字节描述符直到全零；库名取描述符中名字地址指向的以零结尾字符串。

六、导入表项优先用 OriginalFirstThunk，为零时退回 FirstThunk；PE32+ 每项八字节、PE32 每项四字节；最高位为一表示按序号导入，符号写成井号加低十六位序号，同时填入 Ordinal。

七、导入结果按库名、符号升序，同一库同一符号只保留一条；总条数超过 MaxImports 返回 ErrImportLimit；对 ELF 样本调用返回 ErrUnsupportedFormat。

八、FuzzyHash 只覆盖到最后一个完整分块，尾部不足一个分块的追加字节不改变结果；同一输入两次结果必须相同，非空结果不为零，内容不同则不同，空样本返回 ErrTruncated。

九、Stable 只在节区按名字升序且名字非空、导入按库名与符号升序且无重复、模糊哈希非零时返回 true。

十、Extract 串起格式识别、节区、导入与模糊哈希，Digest 为十六位小写十六进制，Seen 置为一，写入特征表；表满返回 ErrFeatureLimit 且不写入。

十一、Features 返回特征表副本；Stats 返回提取次数、拒收次数、节区数、导入数与当前特征数；Capacity 返回特征表上限。

十二、Snapshot 覆盖特征表与统计，Restore 到同配置实例后逐项一致，坏字节返回 ErrBadSnapshot。

硬约束：同一实例上的 Extract 由写锁串行，特征表要么整体写入要么完全不写；所有解析必须先做边界检查；MaxSections 为零按 96、MaxImports 为零按 512、MaxFeatures 为零按 256。

验收标准：go test ./... 的 15 条用例必须全部通过；不得修改测试、跳过用例或按固定输入写死；并发与快照恢复场景自行验证；只做本地实现，不引入第三方库。

## 运行

```bash
go test ./...
```
