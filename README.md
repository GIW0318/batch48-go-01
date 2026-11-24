# batch48-go-game-b-22

## 题面

开镜后的后处理链一降分辨率就出错：景深按半分辨率采样，却把全分辨率的目标当输入，画面边缘糊出一圈；切换画质档之后旧尺寸的中间目标还挂在池子里，显存越用越多，低端机上跑十分钟就报分配失败；超分被插了两次，同一帧多做一遍全屏拷贝。策划新加的暗角效果声明必须原生分辨率，接进链里之后粒子拖尾又开始发糊，玩家反馈开镜时远处树线像蒙了一层雾，而关闭后处理时一切正常。同一份链在编辑器预览里顺序稳定，进真机后 pass 顺序每次启动都不一样，帧抓取工具里能看到同一帧两次全屏拷贝；低配机把缩放压到 0.75，半分辨率效果的目标尺寸出现小数，采样时又取整到相邻像素，边缘抖动明显。落点 gsb/batch48-go-game-b-22 的 postfx_resolution_planner.go，类型 PostFxResolutionPlanner，接口保持：NewPostFxResolutionPlanner(maxTargets int) *PostFxResolutionPlanner、SetScale(scale float64) error、Scale() float64、SetChain(effects []Effect) error、Chain() []Effect、Classify(e Effect) ResolutionClass、Plan(w, h int) ([]Pass, error)、AcquireTarget(k TargetKey) (int, error)、ReleaseTarget(h int) error、TargetSize(h int) (int, int, bool)、SetPassLimit(n int) error、Generation() uint64、TargetCount() int、Stats() PlannerStats。Effect 带 Name、Need、Enabled，Need 取 NeedHalf、NeedNative、NeedForced；Pass 带 Effect、Kind、Class、Width、Height，Kind 取 PassEffect、PassUpscale、PassDownscale；TargetKey 带 Name、Width、Height、Class；PlannerStats 带 Passes、Targets、Rebuilds、Reused。SetScale 成功后 Generation 递增，半分辨率档的宽高按当前缩放系数四舍五入且至少一像素；超分与降采样这类过渡 pass 的 Effect 字段留空。

要修的行为：（1）缩放系数接受 0、负数、超过 1 与 NaN，编排时算出 0 宽的目标；（2）缩放到 1 时所有效果仍是半分辨率档；（3）Classify 把 NeedForced 与 NeedNative 判成半分辨率，半分辨率效果在低缩放下反被判成原生；（4）原生档效果之前没有插入超分，或者连续两个原生档之间重复插入超分；（5）从原生档回到半分辨率档时缺少降采样，档位与尺寸对不上；（6）半分辨率档的宽高按整型截断而不是四舍五入，奇数尺寸差一像素；（7）禁用中的效果仍出现在编排结果里，宽或高非正时也照样返回列表；（8）同一份链与缩放下连续调用两次 Plan，条数或顺序不一致；（9）pass 数超过上限不报错；（10）相同 TargetKey 重复申请拿到不同句柄，目标池被撑爆；（11）目标池超限不报错而是继续分配，释放过的句柄再释放一次也不报错，TargetSize 还能返回旧尺寸；（12）两个强制原生档相邻时，又被插入一次降采样再超分，白白多两遍全屏拷贝。

硬约束：Plan 对同一输入必须逐字段稳定，不依赖内部遍历顺序；目标池容量与 pass 数是硬顶，超限返回可识别错误；缩放变化必须让旧目标整体失效，重新申请才拿到新尺寸。验收：go test ./... 15 条全过，不得修改测试、不得跳过、不得按固定输入写死；缩放切换与目标池自行验证；目标池里的句柄还要能说明自己属于哪次缩放，便于排查旧尺寸残留；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-game-b-22`
- 包名：`task`
- 源码文件：`postfx_resolution_planner.go`
- 测试目录：`tests/`
