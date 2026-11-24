# batch48-go-game-c-05

## 题面

线上教研的混流服务器最近被投诉两次：老师画面在画中画和全屏之间来回跳，切一次要等好几秒才能稳定；上一节课的老师退出后，新老师进来，全屏区里先闪了一下上一位老师的定格画面，之后才变成新画面。还有一次输出帧率设成 60、三路输入都是 30 帧，账单里却按 60 帧全额计费，质量报表里也把重复帧算成了有效画面。请修 gsb/batch48-go-game-c-05 模块里的 video_compositor_guard.go，类型 CompositorGuard，包名 task，只允许使用标准库。

接口保持：NewCompositorGuard(Layout, Config) (*CompositorGuard, error)、AddTrack(Track) error、RemoveTrack(string) error、SetLayout(Layout) error、SetActiveSpeaker(string) error、ActiveSpeaker() string、Compose(uint64, int64) (*Composition, error)、Regions() []Region、TrackRegion(string) (string, bool)、Snapshot() ([]byte, error)、Restore([]byte) error、Stats() GuardStats、Reset() error。Region 含 Name、X、Y、W、H；Layout 含 Name、Regions；Track 含 ID、Width、Height、FPS、Region；Cell 含 Region、TrackID、Filled、Valid、FrameNo；Composition 含 FrameNo、Timestamp、Cells；Config 含 OutputFPS、HysteresisCount；GuardStats 含 Frames、Cells、Filled、InvalidCells、Dropped、SpeakerSwitches、TrackAdds、TrackRemoves。

需要修的行为：

一、构造时校验区域名唯一、宽高为正、矩形互不重叠，违规分别返回 ErrLayoutInvalid 或 ErrRegionOverlap。

二、AddTrack 校验 ID 非空、宽高与 FPS 为正，违规返回 ErrTrackInvalid；重复 ID 返回 ErrTrackExists。

三、Track 指定区域必须存在于模板，否则返回 ErrUnknownRegion；区域已被占用返回 ErrRegionTaken；未指定区域时按模板顺序分配第一个空闲区域。

四、没有空闲区域时 AddTrack 返回 ErrNoRegion，不得挤掉已有轨道；成功加入计入 TrackAdds，第一条加入的轨道默认是当前说话人。

五、RemoveTrack 未知轨道返回 ErrUnknownTrack，成功释放区域并计 TrackRemoves，之后合成不再包含它。

六、SetLayout 整表替换且要么全成要么全不成：新模板非法返回 ErrLayoutInvalid、区域重叠返回 ErrRegionOverlap、轨道分配不下返回 ErrNoRegion，旧布局与旧分配保持不变。

七、SetLayout 成功后，区域仍存在的轨道保留原分配，被删区域的轨道重新分配到空闲区域。

八、SetActiveSpeaker 未知轨道返回 ErrUnknownTrack 且不改变当前说话人；候选需要连续 HysteresisCount 次提名才切换，切换计入 SpeakerSwitches。

九、迟滞期间提名当前说话人清零候选计数，提名另一个候选重新计数；轨道被移除时其迟滞计数必须清零，残留计数会让新加入的轨道被误判为活跃。

十、Compose 的帧序号必须严格递增，零或回退返回 ErrFrameOrder 且不推进状态；输出帧号从 1 开始。

十一、帧率对齐按每输出帧消耗的输入帧数计算：第 n 帧（n 不小于 2）消耗 floor((n-1)*fps/OutputFPS)-floor((n-2)*fps/OutputFPS) 个输入帧，第一帧按一个输入帧计；超过一个的部分丢弃并计入 Dropped，一个都没有时用上一帧填充并标记 Filled；每帧产出的区域条目计入 Cells。

十二、输出帧率高于全部输入时，重复填充出来的帧必须标记为非有效帧，计量与质量统计要能剔除它们；轨道重新加入同一区域时填充状态要重置，不能先显示上一轮遗留的旧帧。

快照与重启：Snapshot 保存轨道、布局、区域分配、说话人、迟滞计数、上一输出帧号与统计，Restore 后 Regions、TrackRegion、ActiveSpeaker 与快照时一致，坏数据返回 ErrStateCorrupt 且不改动内存状态。并发与顺序：AddTrack、RemoveTrack、SetLayout、SetActiveSpeaker 与 Compose 串行执行，Compose 不能看到半个布局；Cells 按模板区域顺序排列。验收：go test ./... 的 15 条用例全过，不得改测试、跳过或按固定输入写死；并发与重启自行验证；只做本地实现。

## 模块与运行

- 模块路径：`gsb/batch48-go-game-c-05`
- 包名：`task`
- 实现文件：`video_compositor_guard.go`
- 测试目录：`tests/`
- 运行全部用例：`go test ./...`
