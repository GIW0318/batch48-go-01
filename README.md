# batch48-go-game-b-13

## 题面

大世界切到新的阴影方案后，远处城墙的阴影每隔几秒整体抖一下，角色站着不动也会出现锯齿爬行。排查发现相机只是平移了几厘米，正交矩阵就偏移不到一个纹素，采样跨了格子。相机远平面从八百米改成九百米后阴影没有重新拟合，边界过渡带宽度仍按旧距离算，画面出现一条硬缝。手动填的切分距离只要有一个等于近平面或顺序填反，整套切分就静默退回默认值，和美术标定的边界对不上。阴影图不是正方形，横向一千零二十四、纵向五百一十二，只按一个纹素尺寸吸附导致纵向仍抖动。相机俯仰接近水平时，级联包围盒纵向几乎退化，矩阵出现零缩放，采样变黑。

请修 gsb/batch48-go-game-b-13 模块里的 shadow_cascade_fit_solver.go，类型名为 CascadeFitSolver，包名 task，只用标准库。接口保持现状：NewCascadeFitSolver(CascadeConfig) *CascadeFitSolver、Split([]float64, float64, float64) ([]float64, error)、Fit(Camera, int) (Mat4, error)、SnapTexel(Mat4, float64) Mat4、SnapTexelXY(Mat4, float64, float64) (Mat4, error)、BlendBand(int) (float64, float64)、SetLambda(float64) error、SetBlendFraction(float64) error、Invalidate(string)、CascadeCount() int、ViewBounds(int) (float64, float64, error)、Stats() FitStats、Version() uint64、Snapshot() ([]byte, error)、Restore([]byte) error、Reset() error。Camera 含 Position、Yaw、Pitch、FovYDeg、Aspect、Near、Far；CascadeConfig 含 Cascades、Lambda、BlendFraction、TexelSize。

需要修的行为：

一、Cascades 小于一或大于八、Lambda 不在零到一之间、BlendFraction 不在零到一之间时，构造出的求解器在 Split 与 Fit 上报 ErrBadConfig。

二、Split 返回长度为 Cascades 加一的边界数组，首元素等于近平面，末元素等于远平面，中间元素严格递增；近平面小于等于零或远平面小于等于近平面返回 ErrBadRange。

三、自动切分按 Lambda 在对数与线性分布间插值，为零退化为纯线性，为一退化为纯对数。

四、distances 非空时按手动边界覆盖自动结果，长度必须等于 Cascades 减一，越界、重复或与近远平面重合返回 ErrBadSplitCount 或 ErrBadRange，顺序填反返回 ErrNonMonotonic。

五、Fit 对指定级联给出正交矩阵，同一相机与级联重复调用逐元素完全一致，命中缓存时 CacheHits 加一。

六、相机近远平面、视场角或宽高比变化后缓存立即失效，Invalidations 加一并 Version 前进。

七、级联编号小于零或不小于 Cascades 返回 ErrBadCascade，相机近平面非正、远平面不大于近平面、视场角不在零到一百八十之间、宽高比非正返回 ErrBadCamera。

八、SnapTexel 只调整矩阵的平移分量，使其落在纹素尺寸的整数倍上，其余元素保持不变；纹素小于等于零时原样返回。

九、BlendBand 返回该级联的过渡带起止比例，非末级为闭区间内的一段，末级返回一比一，非法级联返回零与零。

十、ViewBounds 返回该级联的近远范围，与 Split 的结果一致，非法级联返回 ErrBadCascade。

十一、Invalidate 记录原因并清空缓存，随后 Fit 必须重新计算而不是复用旧矩阵。

十二、Snapshot 保存级联配置、手动边界与版本，Restore 坏数据返回 ErrBadSnapshot 且不改动现有状态。

并发与原子性：Fit 会被主线程与阴影裁剪线程同时调用，同一相机参数下所有调用必须得到完全相同的矩阵，缓存计数不能丢失或重复累加。快照恢复：Restore 后版本与配置延续，不能把已失效的缓存当成有效。性能预算：缓存命中不得重新构造矩阵，重复拟合的时间不能随调用次数增长。

验收方式：go test ./... 的 15 条用例必须全部通过，不得修改用例、跳过或用固定输入写死结果；并发与重启自行验证；只允许本地实现，不引入第三方依赖。

## 模块与运行

- 模块路径：`gsb/batch48-go-game-b-13`
- 包名：`task`
- 实现文件：`shadow_cascade_fit_solver.go`
- 测试目录：`tests/`
- 运行全部用例：`go test ./...`
