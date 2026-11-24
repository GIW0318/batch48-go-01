# batch48-go-safety-c-13

- 模块路径：`gsb/batch48-go-safety-c-13`
- 包名：`task`
- 实现文件：`secret_leak_scanner.go`
- 测试目录：`tests/`

## 运行方式

```bash
go test ./...
```

## 题面

安全审计在代码仓库里扫硬编码凭据，最近一次全量扫描报出两千多条，值班同学逐条看了半天，大半是测试夹具和文档示例；真正的一条生产密钥反而被同一份文件里重复出现的同一个值淹没了，报告里只剩一行没有位置的计数。想加白名单压噪声，结果加了之后新出现的密钥也不再上报，没人说得清哪些是新增、哪些是历史遗留。

请修 gsb/batch48-go-safety-c-13 模块里的 secret_leak_scanner.go，类型名为 SecretLeakScanner，包名 task，只用标准库。接口签名保持现状：NewSecretLeakScanner(ScannerOptions) *SecretLeakScanner、ScanFile(string, []byte) ([]Finding, error)、ScanTree(string) ([]Finding, error)、SetBaseline([]string) error、Allow(Context) error、Findings() []Finding、BaselineDiff() (BaselineDiff, error)、Entropy(string) float64、Fingerprint(Finding) string、Stats() ScannerStats、Snapshot() ([]byte, error)、Restore([]byte) error、Reset() error。Finding 含 RuleID、Path、Value、Occurrences、Entropy；Location 含 Line、Column；Context 含 PathSuffix、Marker、Reason；BaselineDiff 含 Added、Removed；ScannerOptions 含 MaxFileBytes、MaxCandidates、MinEntropy、Prefixes；ScannerStats 含 Files、Candidates、Findings、Suppressed。

需要修的行为：

一、候选是按标点切开的连续串，长度至少 16，并且以配置的前缀开头，或者所在行出现以 password、secret、token、key 结尾的赋值键；前缀命中的规则名为 prefix:前缀，只靠赋值上下文命中的为 generic_secret，两条路径都满足时按前缀记。

二、候选的香农熵按字节频率计算，全等字符为零；低于 MinEntropy 的候选一律不报。

三、同文件同规则同值只产生一条 Finding，Occurrences 按行列升序给出全部位置，行列从 1 开始；候选计数按出现次数累加，去重只作用于结果而不合并计数。

四、Finding 的 Path 取扫描时传入的路径，ScanTree 下取文件名路径；每个结果只属于一个文件。

五、Allow 注册的上下文按路径后缀排除，被排除的文件仍计入文件数但不产生结果，Suppressed 加一；后缀与标记都为空、或原因为空返回 ErrInvalidContext。

六、单文件超过 MaxFileBytes 返回 ErrFileTooLarge，不读内容也不产生结果。

七、单次扫描候选数超过 MaxCandidates 返回 ErrCandidateLimit，本次结果整体丢弃，不静默截断。

八、SetBaseline 接受指纹列表，空串、重复或非 64 位十六进制返回 ErrInvalidBaseline。

九、命中基线的 Finding 不再出现在 Findings 结果里，Suppressed 计数加一，但仍参与消失统计。

十、BaselineDiff 的 Added 是本次新增指纹，Removed 是基线里本次没有再现的指纹，两者升序返回。

十一、ScanTree 按路径字典序遍历目录，聚合结果按路径、规则、值排序；读取失败返回错误且不返回半份结果。

十二、每次 ScanFile 与 ScanTree 整体替换上一轮结果，不累积；Stats 分别统计文件、候选、结果与抑制次数，Reset 清空全部状态。

硬性约束：同一份输入重复扫描必须得到逐条相同的结果，ScanFile 与 Findings、SetBaseline 并发时读方看到的是完整的一轮结果，不会读到写了一半的位置集合。快照恢复后基线、排除上下文与最近一轮结果逐条一致，重启后 BaselineDiff 的回答不变，坏快照返回 ErrBadSnapshot 且不破坏内存状态。

验收：go test ./... 的 15 条用例全过，不改测试、不跳过、不按固定输入写死；并发扫描与重启恢复自行验证；只做本地实现。
