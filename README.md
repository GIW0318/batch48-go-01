# 后处理链编译：效果优先级、逐级分辨率与乒乓目标复用

## 题面

开了泛光以后进游戏窗口一缩到最小，整屏变黑，日志里说采样了一张零尺寸纹理；同一套效果在编辑器里预览正常，打包进游戏就错位；换个场景又发现关掉色调映射后链条中间空了一格，画面直出没有经过后面的抗锯齿，帧时间还比全开时更高。落点 gsb/batch48-go-game-b-10 的 postfx_chain_compiler.go，类型 PostFXChainCompiler，签名保持：NewPostFXChainCompiler() *PostFXChainCompiler、Enable(effect Effect, params Params) error、Disable(effect Effect) error、SetScale(effect Effect, scale float64) error、Enabled() []Effect、ParamsOf(effect Effect) (Params, bool)、SetMaxPasses(n int) error、Compile(w, h int) (Chain, error)、Resize(w, h int) error、Size() (int, int)、InvalidateHistory(reason string)、HistoryValid(effect Effect) bool、LastChain() (Chain, bool)、Snapshot() ([]byte, error)、Restore(payload []byte) error。五个效果是 motion_blur、ssao、bloom、tonemap、fxaa，固定优先级按这个顺序从低到高；Params 带 Scale、In、Out 与 History，格式只有 rgba8、rgba16f、r11g11b10；Pass 带 Effect、Kind、Input、Output、Width、Height，Kind 取 effect 或 convert；Target 带 Name、Kind、Width、Height，Kind 取 temp 或 history。临时目标按 pass 顺序在两张之间轮换，同时存活的临时目标只有两张，需要历史的效果另占两份历史目标；Resize 只记录尺寸，Compile 按传入宽高与每级缩放算目标大小。

要修：未注册的效果名被接受，缩放比例 0 或大于 1 也照收，格式写错也不拦；重复启用同一效果产生两个 pass，禁用未启用的效果不报错；效果顺序跟着启用顺序走，不同启用次序编出不同链；禁用后前后 pass 没有直接对接，中间留着空 pass；相邻 pass 格式不同却直接对接，没有插入转换 pass；每级缩放不生效或向下取整，1920 乘 0.5 编出 959 宽；临时目标每个 pass 新建一份，乒乓复用失效，同时存活目标数不封顶；只启用一个效果时凭空多分配中间目标，链首链尾没有直接读写外部目标；需要历史的效果只分配一份历史，缺少双缓冲；窗口缩到 1x1 时缩放目标算出零尺寸纹理，采样直接崩；尺寸变化或相机切换后继续拿旧帧历史累积，历史既不重置也不作废；编译失败后上一条可用链被清空，快照丢掉效果集与历史有效性。

硬约束：Compile 与 Enable、Disable、Resize、快照在同一把锁下串行，同一份效果集与参数在同一尺寸上编译两次，pass 序列与目标分配逐项一致；每个效果在链里只出现一次，重复启用只更新参数，Enabled 按固定优先级返回且与启用先后无关；每个 pass 的 Input 是上游格式或 external，Output 是临时目标名或 external，相邻格式不一致时插一个只做格式转换的 pass，缩放尺寸向上取整且不得为零；pass 数上限硬顶，超限报错并保留上一条可用链，Compile 收到零或负的宽高直接报错；快照覆盖效果集、参数、尺寸、目标分配与历史有效性，恢复要么整体生效要么完全不生效，内容不完整一律拒绝并保留旧状态。

验收：go test ./... 的 15 条用例全过，不得改测试、跳过或按固定输入写死；并发开关与快照恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-game-b-10`
- 实现文件：`postfx_chain_compiler.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`PostFXChainCompiler`、`Effect`、`Params`、`Pass`、`Target`、`Chain`
