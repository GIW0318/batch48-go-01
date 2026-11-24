# batch48-go-game-b-15

## 题面

材质编辑器的同事把一个磨损材质从测试工程搬到正式关卡，同一个图烘焙出来的哈希每次都变，热更新通道认为材质一直在改，反复重下发。节点图里一个常量被三个混合节点引用，烘焙耗时按引用次数线性增长，改一个常量要等好几秒。有人不小心把两个节点连成环，烘焙直接卡住没有返回，编辑器只能强杀。端口类型不匹配时烘焙默默把颜色端口当成数值用，出来的金属度全是零。删掉一个中间节点后，下游输入悬空，烘焙仍然成功，结果里少了整段花纹。只改了一个分支的参数，按说只该重算下游几个节点，实际整张图全部重来。

请修 gsb/batch48-go-game-b-15 模块里的 material_graph_baker.go，类型名为 MaterialGraphBaker，包名 task，只用标准库。接口保持现状：NewMaterialGraphBaker(string) *MaterialGraphBaker、AddNode(Node) error、RemoveNode(string) error、Connect(Port, Port) error、Disconnect(Port) error、Bake() (*MaterialDef, error)、Invalidate(string) error、InvalidateAll() error、Order() ([]string, error)、Node(string) (Node, bool)、SetKeywords([]string) error、Keywords() []string、Stats() BakerStats、Snapshot() ([]byte, error)、Restore([]byte) error、Reset() error。Node 含 ID、Kind、Params 与 OutputType；Port 含 Node、Name、Type；MaterialDef 含 Name、Hash、Order、Values、Keywords、Version。

需要修的行为：

一、AddNode 的 ID 为空返回 ErrBadNode，ID 重复返回 ErrNodeExists，Kind 不属于 constant、add、mul、clamp 返回 ErrUnknownKind；OutputType 为空表示 float。

二、Connect 的两个端点任一不存在返回 ErrNodeMissing 且消息里带出节点编号，端口名不是该节点的合法端口返回 ErrUnknownPort。

三、两端端口类型不一致返回 ErrPortType，同一输入端口已有连接返回 ErrInputTaken，连接会形成环返回 ErrCycle 且图保持连接前的状态。

四、Disconnect 释放指定输入端口，未连接的端口返回 ErrUnknownPort，释放后该输入变为悬空。

五、Bake 遇到非 constant 节点存在未连接输入时返回 ErrDangling，错误消息必须指出具体节点编号。

六、Bake 按拓扑序求值，constant 取 Params 的 value，add 求输入之和，mul 求输入之积，clamp 把输入夹到零到一。

七、同一节点被多条路径引用时只求值一次，结果按节点编号稳定排序写入 MaterialDef.Order 与 Values。

八、关键字集合去空白、转小写、去重并升序后参与哈希，关键字顺序不同但集合相同的两次 Bake 得到相同 Hash。

九、相同图重复 Bake 逐字段一致，Version 只在图结构、参数或关键字变化后前进，重复烘焙不改变 Version。

十、Invalidate 对未知编号返回 ErrNodeMissing，对已知节点标记其自身与所有下游为待重算，其余节点结果保留。

十一、RemoveNode 对仍被引用的节点返回 ErrNodeHasDependents，对叶子节点删除成功并从输入连接中移除。

十二、空图 Bake 返回 ErrEmptyGraph，非法 Kind 或缺失节点不得用默认值静默填充。

并发与原子性：编辑器线程会同时增删节点、连接端口并触发烘焙，任何一次 Bake 只能看到修改前或修改后的完整图，不能拿到半张图的求值结果；烘焙过程不得阻塞查询节点。幂等与顺序：同一张图连续烘焙必须产生相同哈希与相同顺序。快照恢复：Snapshot 保存节点、连接、关键字与版本，Restore 坏数据返回 ErrBadSnapshot 且不改动现有图。

验收方式：go test ./... 的 15 条用例必须全部通过，不得修改用例、跳过或用固定输入写死结果；并发与重启自行验证；只允许本地实现，不引入第三方依赖。

## 模块与运行

- 模块路径：`gsb/batch48-go-game-b-15`
- 包名：`task`
- 实现文件：`material_graph_baker.go`
- 测试目录：`tests/`
- 运行全部用例：`go test ./...`
