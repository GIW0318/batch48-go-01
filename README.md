# batch48-go-safety-c-10

- 模块路径：`gsb/batch48-go-safety-c-10`
- 包名：`task`
- 实现文件：`attack_surface_mapper.go`
- 测试目录：`tests/`

## 题面

季度攻防演练前的暴露面盘点对不上账：安全同学手工画出来的可达路径有十七条，平台只报出九条，漏掉的都是跨了两三个跳板机的中转路径；另一批资产在上周下线之后仍然挂在暴露清单里，值班同学照着清单去核对，发现端口早就关了。更麻烦的是撤销一条临时放行策略之后，平台要重启才认账，演练当天改策略，看板半小时没更新。

请修 gsb/batch48-go-safety-c-10 模块里的 attack_surface_mapper.go，类型名为 AttackSurfaceMapper，包名 task，只用标准库。接口签名保持现状：NewAttackSurfaceMapper(MapperOptions) *AttackSurfaceMapper、Upsert(Asset) error、Remove(string) error、Asset(string) (Asset, bool)、AddPolicy(Policy) error、RemovePolicy(string, string, int) error、Policies() []Policy、Reachable(Entry) ([]Path, error)、Exposures() []Exposure、Diff(Snapshot) (Delta, error)、Snapshot() (Snapshot, error)、Restore(Snapshot) error、Stats() MapperStats、Reset() error。Asset 含 ID、Tenant、Kind、Ports、Exposed；Policy 含 From、To、Port、Allow、CrossTenant；Entry 含 Asset、Port；Path 含 Nodes、Hops、Port；Exposure 含 Asset、Tenant、Port；Snapshot 含 Version、Assets、Policies、Exposures。

需要修的行为：

一、Upsert 校验：ID 或租户为空、任一端口不在 1 到 65535 返回 ErrInvalidAsset；端口去重后升序保存；超过 MaxAssets 返回 ErrAssetLimit 且不写入。

二、同一 ID 再次 Upsert 视为替换，端口、类型、暴露标记整体覆盖，资产总数不变；每次成功写入把内部版本号加一。

三、Remove 删除资产并同时删除所有以它为起点或终点的策略，未知 ID 返回 ErrUnknownAsset。

四、AddPolicy 要求两端资产都存在，否则返回 ErrUnknownAsset；起点与终点相同返回 ErrSelfPolicy；端口不在合法区间返回 ErrInvalidPolicy；同起点同终点同端口的策略重复返回 ErrDuplicatePolicy；超过 MaxPolicies 返回 ErrPolicyLimit。

五、RemovePolicy 按起点、终点、端口三元组删除，未命中返回 ErrUnknownPolicy；Policies 按起点、终点、端口升序返回副本。

六、Reachable 先校验入口资产存在、入口端口属于该资产，否则分别返回 ErrUnknownAsset 与 ErrUnknownPort；只沿 Allow 为真的策略推导，策略端口为零表示任意端口，否则必须等于入口端口。

七、跨租户边只有 CrossTenant 为真时才可通行；同一目标资产即使有多条路径，也只保留跳数最少的一条，跳数相同取节点序列字典序最小的一条。

八、结果按跳数升序、末节点字典序升序返回，节点序列从入口资产开始，Path 的 Port 取入口端口；搜索深度超过 MaxHops 立即返回 ErrHopLimit。

九、暴露面按资产加端口展开：Exposed 为真的资产每个端口一条记录，按租户、资产、端口升序；同一资产只开放了部分端口时，差异只报变化的端口，不能把整台资产算作新增或消失。

十、Diff 以入参快照为基线，只报新增与消失的暴露项，各自按租户、资产、端口升序；快照版本不大于零返回 ErrBadSnapshot。

十一、策略删除立即生效，同一实例上后续 Reachable 立刻不再包含被切断的目标，不需要重建实例；Snapshot 覆盖版本、资产、策略与暴露面，Restore 后可达集合与在线结果逐条一致，且对同一快照的 Diff 为空。

十二、Restore 要校验版本为正、资产合法且唯一、策略两端存在，否则返回 ErrBadSnapshot；Reset 之后版本从零重新计数；Stats 返回资产数、策略数、暴露项数与当前版本号。

硬约束：图读写由同一把锁串行，Reachable 与 Upsert 并发时只能看到完整状态，不能读到写了一半的资产或策略；快照必须整体生成，不允许版本号与内容对不上；MaxAssets 为零按 512、MaxPolicies 为零按 2048、MaxHops 为零按 8。

验收标准：go test ./... 的 15 条用例必须全部通过；不得修改测试、跳过用例或按固定输入写死；并发与快照恢复场景自行验证；只做本地实现，不引入第三方库。

## 运行

```bash
go test ./...
```
