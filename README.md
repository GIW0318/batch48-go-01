# batch48-go-safety-c-12

- 模块路径：`gsb/batch48-go-safety-c-12`
- 包名：`task`
- 实现文件：`ioc_feed_normalizer.go`
- 测试目录：`tests/`

## 运行方式

```bash
go test ./...
```

## 题面

情报平台接入第三家 IOC 源之后，匹配结果开始打架：同一个地址在一家源里写成网段、另一家写成单 IP，归一之后剩下两条；域名大小写和结尾的点不一致，查不到；一个指标既出现在域名源又出现在地址源，归一流程把它当域名塞进集合，地址匹配整段失效。值班同学更担心的是限额：有一次上游推来一个二十万条的包，进程内存直接翻倍，旧集合也被冲掉，之后所有查询都回答未命中。

请修 gsb/batch48-go-safety-c-12 模块里的 ioc_feed_normalizer.go，类型名为 IocNormalizer，包名 task，只用标准库。接口签名保持现状：NewIocNormalizer(NormalizerOptions) *IocNormalizer、Ingest(Feed) error、Normalize(Indicator) (Indicator, error)、Merge() (*IocSet, error)、Lookup(string) ([]Source, bool)、Entries() []IocEntry、DropSource(string) error、Stats() NormalizerStats、Snapshot() ([]byte, error)、Restore([]byte) error、Reset() error；IocSet 另有 Len() int 与 Values() []string。Indicator 含 Value、Kind、TTL、Source；Feed 含 Name、DefaultTTL、Indicators；Source 含 Name、TTL；IocEntry 含 Value、Kind、TTL、Sources；NormalizerOptions 含 MaxEntries、MaxPerFeed；NormalizerStats 含 Feeds、Ingested、Rejected、Entries。

需要修的行为：

一、Normalize 去掉首尾空白；域名转小写并去掉结尾的点；URL 拆出协议与主机，协议和主机转小写，路径保持原样；IP 与网段按网络地址规范化，主机位清零。

二、Kind 为空时自动判定：能解析成地址或网段为 ip，带协议分隔符为 url，其余为 domain；显式 Kind 与判定结果不一致返回 ErrTypeConflict。

三、空值、TTL 为负、域名标签非法都返回 ErrInvalidIndicator。

四、Ingest 校验源名称与指标列表，名称为空返回 ErrInvalidFeed；单源指标数超过 MaxPerFeed 返回 ErrFeedTooLarge，整源拒绝且不改变已有数据。

五、Ingest 原子：任一指标非法则整源拒绝，已成功摄入的其它源保持原样，失败源的旧数据也保留。

六、同一源重复 Ingest 视为整源替换，旧指标不残留。

七、Merge 合并同一指标的多源记录，保留来源列表并按来源名升序，TTL 取各来源的最小值；完全没有摄入任何源时返回空集合而不是错误。

八、网段合并：同族前缀存在包含关系时保留较大的网段，相邻且能合成一个前缀的两条合并为一条；合并结果写入上一次成功集合。

九、条目顺序稳定：先按 Kind 再按 Value 升序，Values 与 Entries 顺序一致，重复调用结果相同。

十、合并后条目数超过 MaxEntries 返回 ErrSetLimit，上一次成功集合保持不变，Lookup 继续按旧集合回答。

十一、Lookup 对地址按网段包含判断，对域名与 URL 按规范化后的值精确匹配，返回来源副本与是否命中；通配域名条目按后缀匹配，精确条目优先于通配条目。

十二、DropSource 删除来源并要求重新 Merge 生效，未知来源返回 ErrUnknownSource。

硬性约束：Ingest、Merge 与 Lookup 并发时，查询只能看到上一份完整集合或新一份完整集合，不得读到半份；快照恢复后来源、指标、TTL 与集合逐条一致，重启后 Lookup 结果与中断前相同，坏快照返回 ErrBadSnapshot 且不破坏内存状态。

验收：go test ./... 的 15 条用例全过，不改测试、不跳过、不按固定输入写死；并发查询与重启恢复自行验证；只做本地实现。
