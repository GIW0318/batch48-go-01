# 屏幕 tile 实例分箱：跨格覆盖、桶容量溢出与逐帧可复现顺序

## 题面

美术外包进的新场景一开镜就像纸片，墙贴边缘缺一块、远处物件忽隐忽现，摄像机一平移还能看到几帧前就该消失的物件残影。查下来是软件光栅器把可见实例按中心点只塞进一格，跨格的物件在别的格子里整块空着，同一帧两次构建出来的桶内顺序又对不上，桶满之后被挤掉的实例干脆没人管。落点 gsb/batch48-go-game-b-06 的 instance_tile_binner.go，类型 InstanceTileBinner，签名保持：NewInstanceTileBinner(tileSize, capacity int) *InstanceTileBinner、Add(inst Instance) (Handle, error)、Remove(h Handle) error、Move(h Handle, bounds Rect) error、Build(view Viewport) (Bins, error)、SetTileSize(px int) error、SetCapacity(n int) error、SetInstanceLimit(n int) error、TileSize() int、Capacity() int、HandleCount() int、BoundsOf(h Handle) (Rect, bool)、TileHandles(key TileKey) []Handle、OverflowHandles() []Handle、Stats() BinStats、Snapshot() ([]byte, error)、Restore(payload []byte) error。Instance 由 Bounds 与 Layer 组成，Rect 是屏幕像素范围且 MinX 不得大于 MaxX，Viewport 给当帧画布宽高，Bins 带 TileSize、Cols、Rows、Tiles 与 Overflow，桶内按句柄升序，Cols 与 Rows 由画布尺寸除以 tile 边长向上取整得到，落在网格之外的格子不进表，Stats 的 Instances 记注册总数、Placements 记实际写入桶的条目数、Overflow 记全局表条数。

要修：句柄不从 1 开始单调递增，删掉再注册会复用旧号；空包围盒、反向包围盒、未知句柄的 Remove 与 Move 全部静默通过；实例表没有上限，tile 尺寸或桶容量设成 0 也照收；宽高为零或负的画布仍能构建出网格；覆盖多个格子的实例只进中心那一格，其余格子空着；包围盒只压住格子一角时被漏掉，只能靠中心点判定；桶内顺序跟着 map 遍历漂移，同一份输入两帧结果不同；桶满后新实例被丢弃，全局溢出表始终为空；溢出实例只在全局表留一条、桶内没有对应标记，删掉它时桶内标记与全局表只清一处；同一实例跨格时在某一格里出现两次，重复写入变成追加而不是幂等；移走实例后旧格子里的句柄还留着，重新构建能看到幽灵；快照丢掉包围盒，恢复后 BoundsOf 全部失联，损坏内容还会把现有表清掉一半。

硬约束：Add、Remove、Move、Build 在同一把锁下串行，并发注册拿到的句柄不能重复，实例表也不能超出上限；构建与增删实例并发时，读方不能看到只写了一半的桶表；构建返回的桶表是当帧快照，之后再改实例表不影响已经交出去的结果；快照覆盖实例表、tile 尺寸与桶容量，恢复要么整体生效要么完全不生效，内容不完整一律拒绝并保留旧状态；同一份实例表连续两次构建逐项一致，桶内顺序与遍历顺序无关；实例数、桶容量、tile 尺寸都是硬上限，超限报错而不是部分写入。

验收：go test ./... 的 15 条用例全过，不得改测试、跳过或按固定输入写死；并发注册与快照恢复自行验证；只做本地实现。

## 运行方式

```bash
go test ./...
```

## 模块与包

- 模块路径：`gsb/batch48-go-game-b-06`
- 实现文件：`instance_tile_binner.go`
- 包名：`task`；测试目录 `tests/` 同样声明 `package task`，通过导入路径引用本模块
- 主要类型：`InstanceTileBinner`、`Instance`、`Rect`、`TileKey`、`Viewport`、`Bins`、`BinStats`、`Handle`
