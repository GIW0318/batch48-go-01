# batch48-go-game-c-15

## 题面

竖屏直播上线后，主播端反馈旋转 90 度后裁剪框整体偏到画面外，运营在后台改裁剪范围也救不回来；部分奇数分辨率机型画面右边和下边出现一条绿边，色度块对不齐；同一条流在预览和落盘两条路径上给出的宽高不一致，播放器按元数据摆放后发现画面被裁掉一角；还有把水平镜像与垂直镜像前后脚都打开的配置，滤镜链叠了两层，画面转回来时比预期多转了一圈。请修 gsb/batch48-go-game-c-15 模块里的 display_transform_guard.go，类型 TransformChain，包名 task，只允许使用标准库。

接口保持：NewTransformChain(src Size, align int) (*TransformChain, error)、SetSourceSize(Size) error、SetAlign(int) error、SetTransform(Transform) error、Normalize(Transform) (Transform, error)、Apply(Transform) (Result, error)、OutputSize() (Size, error)、Current() Transform、Reset() error、Snapshot() ([]byte, error)、Restore([]byte) error、Stats() ChainStats。Transform 含 Rotation、FlipH、FlipV、Crop；Result 含 Output、Crop、Requested、Aligned；Size 含 W、H；Rect 含 X、Y、W、H；ChainStats 含 Applied、Normalized、Merged、Rejected、Restored。

需要修的行为：

一、源尺寸宽高必须为正，否则返回 ErrTransformSize；对齐只接受 1、2、4、8，否则返回 ErrTransformAlign。

二、旋转只接受 90 的整数倍并折算到 0 到 359，非整数倍返回 ErrTransformRotation。

三、FlipH 与 FlipV 同时为真等价于再旋转 180 度，Normalize 必须合并成一条，不能两条都保留。

四、裁剪框以源像素坐标给出，必须完整落在源画面内，越界返回 ErrTransformCrop；零值裁剪表示整帧。

五、90 度与 270 度旋转后宽高互换，裁剪框的宽高也随之交换，OutputSize 返回旋转并裁剪后的输出尺寸。

六、裁剪偏移按 align 向下取整，裁剪宽高按 align 向上取整；显式裁剪必须能放回旋转后的画面，越界时先平移偏移。

七、整帧输出按旋转后的宽高向上取整到 align 的整数倍，奇数分辨率允许边缘补齐，不得直接丢掉一个像素。

八、Apply 返回的 Output 必须等于 Crop 的宽高，Crop 必须落在旋转后画面内；Aligned 标记本次是否做过对齐调整。

九、应用幂等：同一变换连续两次 Apply 结果一致，Normalize 不改变已经规范的变换。

十、裁剪后旋转与旋转后裁剪结果不同，链上顺序不规范的输入要换算坐标完成等价改写，不能直接拒绝。

十一、对齐调整导致裁剪偏移时元数据要保留原始请求坐标，便于回溯到用户输入的裁剪框。

十二、快照保存源尺寸、对齐、当前变换与全部计数，恢复后一致；坏数据 Restore 返回 ErrTransformSnapshot 且不改现状。

并发与硬约束：SetTransform 与 OutputSize 在同一把锁内串行，任何读方只能看到一次完整提交；等价变换先合并再应用，不允许把叠加的镜像和旋转原样下发；宽高与对齐都是硬校验，宁可报错也不静默截断。

验收：go test ./... 的 15 条用例全过，不得改测试、跳过或按固定输入写死；并发改变换与旋转后裁剪自行验证；只做本地实现。

## 模块与运行

- 模块路径：`gsb/batch48-go-game-c-15`
- 包名：`task`
- 实现文件：`display_transform_guard.go`
- 测试目录：`tests/`

```bash
go test ./...
```
