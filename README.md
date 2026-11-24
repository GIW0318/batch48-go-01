# batch48-go-safety-c-05

模块路径 `gsb/batch48-go-safety-c-05`，包名 `task`，实现文件 `vuln_risk_ranker.go`，主类型 `VulnRiskRanker`。

## 题目

漏洞管理平台每周出修复队列，安全团队对排序结果越来越不信任：同一批漏洞在两次刷新里顺序来回跳，评分明明相同却一次排前一次排后；权重从三成改到五成后，全量重算和逐条算出来的分数对不上，有人手算复核发现差了几分；EPSS 缺失的漏洞被直接当零分排到队尾，可同组其他同类漏洞其实风险很高；同一个 CVE 命中三台资产时，队列里出现三条重复条目，去重全靠人工；队列上限设成五百条，第六百条被直接丢掉，连丢弃条数都没有记录；分数用浮点累加，批量算到后面出现小数漂移，同一漏洞两次得分不同，值班同学只能手工重排一遍，评审会上被追问依据时拿不出可复算的账。

请修 `gsb/batch48-go-safety-c-05` 模块里的 `vuln_risk_ranker.go`，类型名 `VulnRiskRanker`，包名 `task`，只用标准库。

接口保持：`NewVulnRiskRanker(Options) *VulnRiskRanker`、`AddAsset(Asset) error`、`Assets() []Asset`、`Score(v Vuln, a Asset) (Score, error)`、`Rank([]Vuln) ([]Ranked, error)`、`Rebalance(Weights) error`、`TopK(k int) ([]Ranked, error)`、`Truncated() int`、`Result() []Ranked`、`Weights() Weights`、`Reset() error`、`Stats() RankerStats`。

所有分值都是千分制整数：CVSS、EPSS、暴露面、资产重要度取值 `0..1000`，权重三项之和必须正好 1000。

要落到的十一条：

一、`Rebalance` 权重和不是 1000 返回 `ErrWeightsSum`；任一分量为负返回 `ErrWeightRange`。

二、`AddAsset` 重复 ID 返回 `ErrDuplicateAsset`；暴露面或重要度越界返回 `ErrAssetRange`；`Assets` 按 ID 升序返回。

三、`Score` 的资产必须在册，否则返回 `ErrUnknownAsset` 并带 ID；CVE 为空或 CVSS、EPSS 越界返回 `ErrBadVuln`。

四、可利用性先由 CVSS 与 EPSS 取均值，已知在野利用再加 150 并封顶 1000；最终分数等于三项乘积求和后整除 1000，全程整数。

五、同一输入重复调用 `Score` 得到同一个整数，不允许浮点中间量；同一漏洞重复入队也不得因为累加顺序不同而变分。

六、`Rank` 输入为空返回 `ErrEmptyInput`；条数超过 `MaxQueue` 返回 `ErrQueueFull`，不得静默截断。

七、`Rank` 按分数降序，同分先按 CVE 升序、再按首个资产 ID 升序，输出稳定。

八、同一个 CVE 命中多台资产时合并成一条，分数取各资产最高值，资产列表按 ID 升序去重后保留。

九、`Rebalance` 之后全量重算的结果必须与逐条调用 `Score` 再排序一致，不能留下旧分数。

十、`TopK` 的 k 小于等于零返回 `ErrBadK`；未调用过 `Rank` 返回 `ErrEmptyRanking`；截断时 `Truncated` 记录被截掉的条数，k 大于总数时 `Truncated` 归零。

十一、EPSS 缺失时按同组同类漏洞的期望值回填而不是置零，回填值写进统计便于复核。

硬约束：权重变更、排序、截断都必须整批原子完成，中途不得让调用方看到新旧混合的队列；同一批输入在两次刷新、两个进程里排序必须一致；`Reset` 后权重回到默认值、队列与统计清零。验收：`go test ./...` 十五条必须全过，不改测试、不跳过、不按固定输入写死；重复计算与权重回放自行验证；只做本地实现。

## 运行

```
go test ./...
```
