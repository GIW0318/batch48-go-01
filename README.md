# batch48-go-game-b-12

## 题面

夜市关卡上线后，画面里的灯总是在临界亮度附近反复亮灭：同一盏路灯这一帧进阴影列表，下一帧又被踢出去，阴影边缘跟着一跳一跳。美术按类别配了配额，结果招牌灯一多，角色脚下的地面补光全被挤掉，人物像浮在半空；把总预算调大之后，已经在淡出的灯又立刻满亮度回来，过渡完全断掉。策划改了曝光参数重新提交灯表，着色列表的顺序每帧都不一样，截图比对永远对不上。还有个更隐蔽的问题：被换下的灯直接在下一帧消失，没有任何淡出时间，录屏里看起来像灯被瞬间掐灭。另外场景里灯数上千时，单帧评估全部灯导致主线程卡顿。

请修 gsb/batch48-go-game-b-12 模块里的 light_budget_arbiter.go，类型名为 LightBudgetArbiter，包名 task，只用标准库。接口保持现状：NewLightBudgetArbiter(ArbiterConfig) *LightBudgetArbiter、Submit([]Light) error、Select(Budget) (Selection, error)、AdvanceFrame(float64) error、Explain(int) (Reason, error)、SetHysteresis(float64) error、SetFadeSeconds(float64) error、Frame() int、LastSelection() (Selection, bool)、FadingLights() []int、Stats() ArbiterStats、Snapshot() ([]byte, error)、Restore([]byte) error、Reset() error。Light 含 ID、Category、Coverage、Intensity、ShadowCapable；Budget 含 Shaded、Shadowed、MaxEvaluations 与 CategoryQuota；Selection 含 Shaded、Shadowed、Fading、Frame、Version；Reason 含 ID、Category、Score、Shaded、Shadowed、Fading、Note。

需要修的行为：

一、Submit 整批替换候选灯表，编号重复返回 ErrDuplicateLight，覆盖率为负返回 ErrBadBudget，空表允许提交但 Select 返回 ErrNoLights。

二、重要性评分等于屏幕覆盖率乘以强度，排序固定为评分降序、编号升序，同一输入两次 Select 结果完全一致。

三、Shaded 数量不超过 Budget.Shaded，Shadowed 必须是 Shaded 的子集且不超过 Budget.Shadowed，只有 ShadowCapable 的灯能进入 Shadowed。

四、每个类别受 CategoryQuota 独立限制，配额为零表示该类不参与，配额缺省时使用配置里的默认配额。

五、评分差距在 SetHysteresis 设定的滞回带以内时保留上一帧已入选的灯，超出滞回带才换人。

六、被换下的灯进入 Fading 列表而不是立刻消失，AdvanceFrame 按 dt 推进淡出进度，累计达到 FadeSeconds 后从 Fading 移除。

七、预算变化后重新仲裁，但在途淡出状态必须保留，不得因为重新 Select 就把 Fading 清空。

八、AdvanceFrame 拒绝负数与零步长，返回 ErrBadDelta；帧号只在该方法里递增。

九、Explain 对未知编号返回 ErrUnknownLight，对已提交的灯给出评分、类别与是否入选、是否投影、是否在淡出。

十、单帧评估数量不得超过 Budget.MaxEvaluations，超出时按评分从高到低截断候选集，未评估的灯保留上一帧结论。

十一、SetHysteresis 与 SetFadeSeconds 拒绝负值，连续设置相同值不改变帧号与选择版本。

十二、Snapshot 保存候选表、选择结果、淡出进度与帧号，Restore 失败时保持原状态并返回 ErrBadSnapshot。

并发与原子性：Submit、Select、AdvanceFrame 可能来自渲染线程与资源加载线程，任意时刻查询只能看到一次完整仲裁的结果，不能看到 Shaded 与 Shadowed 来自不同帧。幂等与顺序：同一帧内重复调用 Select 不得推进帧号或重置淡出进度。资源上限：评估上限与总预算都必须生效，候选灯表变化后不得让上帧的临时缓冲无限增长。

验收方式：go test ./... 的 15 条用例必须全部通过，不得修改用例、跳过或用固定输入写死结果；并发与重启自行验证；只允许本地实现，不引入第三方依赖。

## 模块与运行

- 模块路径：`gsb/batch48-go-game-b-12`
- 包名：`task`
- 实现文件：`light_budget_arbiter.go`
- 测试目录：`tests/`
- 运行全部用例：`go test ./...`
