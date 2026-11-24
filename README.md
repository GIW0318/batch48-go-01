# batch48-go-game-b-14

## 题面

角色贴着墙根走的时候，脚下接触阴影和地面之间裂开一条亮缝，远看像整个人飘起来；美术把偏置调小，裂缝没了，可墙面又出现大片自阴影噪点。同一个材质在近处级联和远处级联上的偏置差了一倍多，级联交界处出现一条明暗突变。移动端把阴影图缩小后纹素世界尺寸变大，偏置却还是旧值，因为级联缩放改了但缓存没刷新。探照灯和点光源共用一套系数，聚光灯边缘的漏光比方向光严重得多。还有一次参数表里纹素尺寸填了零，求解器返回 NaN，整帧颜色变成一片白。法线偏移和深度偏置被叠加了两次，接触阴影的位置明显偏移。

请修 gsb/batch48-go-game-b-14 模块里的 shadow_bias_resolver.go，类型名为 ShadowBiasResolver，包名 task，只用标准库。接口保持现状：NewShadowBiasResolver(ResolverConfig) *ShadowBiasResolver、Bias(int, LightKind, float64) (float64, error)、NormalOffset(float64, float64) float64、NormalOffsetChecked(float64, float64) (float64, error)、Clamp(float64, float64) (float64, float64)、Combine(float64, float64) CombinedBias、SetCascadeScale([]float64) error、CascadeScale(int) (float64, error)、Coefficients(LightKind) (BiasCoefficients, error)、Version() uint64、Stats() ResolverStats、Reset() error、Snapshot() ([]byte, error)、Restore([]byte) error。LightKind 取 directional、spot、point 三个常量；BiasCoefficients 含 Constant、Slope、Min、Max、NormalScale；CombinedBias 含 Bias、NormalOffset、Total。

需要修的行为：

一、Bias 按级联缩放、光源类型系数与斜率计算，缩放与斜率越大偏置越大，方向光系数最高、聚光灯次之、点光源最低。

二、Bias 结果限制在配置的最小值与最大值之间，最小值不得低于让接触阴影贴合地面的下限，任何输入都不得返回 NaN 或无穷。

三、级联编号越界返回 ErrBadLevel，未知光源类型返回 ErrUnknownKind，斜率为负或超过上限返回 ErrBadSlope，级联缩放小于等于零返回 ErrZeroTexel。

四、NormalOffset 等于纹素世界尺寸乘以法线缩放再乘以一加斜率，纹素与斜率都非负时结果单调不减。

五、NormalOffsetChecked 在纹素小于等于零时返回 ErrZeroTexel，斜率越界时返回 ErrBadSlope，合法输入与 NormalOffset 结果一致。

六、Clamp 把深度偏置夹到配置的最小值与最大值之间，把法线偏移夹到非负范围与合理上限，两者分开返回，不得互相吸收。

七、Combine 返回深度偏置、法线偏移与两者之和，合并只做一次加法，不得把法线偏移重复计入深度偏置。

八、SetCascadeScale 接受一到八级非零正缩放，长度为零、含非正值或超长返回 ErrBadScale，成功后 Version 前进且旧偏置不得残留。

九、CascadeScale 对非法级联返回 ErrBadLevel，返回值与设置时的数组逐项一致。

十、Coefficients 返回该光源类型的系数副本，未知类型返回 ErrUnknownKind，修改返回值不得影响求解器内部状态。

十一、Stats 的 Resolves 统计成功求值次数，Recomputes 在级联缩放变化后递增，Version 与 SetCascadeScale 一致。

十二、Snapshot 保存配置、缩放数组与版本，Restore 坏数据返回 ErrBadSnapshot 且保持原状态，Reset 恢复构造时的默认缩放。

并发与原子性：多个渲染线程会同时查询不同级联的偏置，设置缩放时要么全部线程看到旧数组要么全部看到新数组，不能出现一半新一半旧；查询不得修改共享状态。快照恢复：Restore 后版本延续，缓存与缩放数组一起替换。资源上限与数值安全：级联数组最多八项，所有计算在极端输入下仍返回有限值。

验收方式：go test ./... 的 15 条用例必须全部通过，不得修改用例、跳过或用固定输入写死结果；并发与重启自行验证；只允许本地实现，不引入第三方依赖。

## 模块与运行

- 模块路径：`gsb/batch48-go-game-b-14`
- 包名：`task`
- 实现文件：`shadow_bias_resolver.go`
- 测试目录：`tests/`
- 运行全部用例：`go test ./...`
