# Auger 详细设计文档

## 1. 模块详细设计

### 1.1 cmd 模块（命令层）

#### 1.1.1 命令组织结构

```
cmd/
├── root.go          # 根命令定义，使用 cobra 框架
├── decode.go        # 解码命令实现
├── encode.go        # 编码命令实现
├── extract.go       # 数据提取命令实现
├── analyze.go       # 数据分析命令实现
├── checksum.go      # 校验和计算命令实现
├── recover2preRevision.go   # 恢复到指定版本
├── recoverFromOtherDb.go    # 跨库恢复
└── version.go       # 版本命令
```

#### 1.1.2 decode 命令详细设计

**功能**: 将 Kubernetes 二进制存储数据解码为人类可读的 YAML/JSON 格式

**关键数据结构**:
```go
type decodeOptions struct {
    out           string  // 输出格式: yaml|json|proto
    metaOnly      bool    // 仅输出元数据
    inputFilename string  // 输入文件路径
    batchProcess  bool    // 批处理模式（用于 etcd-dump-logs）
}
```

**处理流程**:
1. 验证输出格式参数
2. 如果是批处理模式，逐行读取 stdin 并解码
3. 否则读取文件或 stdin 的完整输入
4. 检测输入格式（二进制或 JSON）
5. 提取并解码数据
6. 输出到指定格式

**批处理模式**:
- 设计用于与 `etcd-dump-logs` 工具集成
- 输入: 十六进制编码的行
- 输出格式: `OK|<decoded_data>` 或 `ERROR:<error_msg>|`

#### 1.1.3 extract 命令详细设计

**功能**: 直接从 BoltDB 文件提取 Kubernetes 数据

**关键数据结构**:
```go
type extractOptions struct {
    out          string  // 输出格式
    filename     string  // BoltDB 文件路径
    key          string  // 要查找的 etcd key
    version      string  // 指定版本
    revision     int64   // 指定修订版本
    keyPrefix    string  // key 前缀过滤
    listVersions bool    // 列出所有版本
    leafItem     bool    // 从 BoltDB leaf item 读取
    printKey     bool    // 打印 key
    metaSummary  bool    // 打印元数据摘要
    raw          bool    // 不解码原始输出
    fields       string  // 输出字段列表
    template     string  // Go 模板字符串
    filter       string  // 过滤条件
}
```

**使用模式**:
1. **单 key 查询**: `-k <key>` 获取特定 key 的最新值
2. **版本列表**: `-k <key> --list-versions` 列出所有版本
3. **前缀查询**: `--keys-by-prefix <prefix>` 按前缀列出 keys
4. **模板输出**: `--template="{{.Value.metadata.name}}"` 自定义输出格式
5. **字段过滤**: `--filter=".Value.metadata.namespace=default"` 过滤结果
6. **Leaf item**: `--leaf-item` 从 BoltDB page item 提取

### 1.2 pkg/encoding 模块（编解码层）

#### 1.2.1 核心常量定义

```go
const (
    StorageBinaryMediaType = "application/vnd.kubernetes.storagebinary"
    ProtobufMediaType      = "application/vnd.kubernetes.protobuf"
    YamlMediaType          = "application/yaml"
    JsonMediaType          = "application/json"

    ProtobufShortname = "proto"
    YamlShortname     = "yaml"
    JsonShortname     = "json"
)

// Protobuf 编码前缀: "k8s\0"
var ProtoEncodingPrefix = []byte{0x6b, 0x38, 0x73, 0x00}
```

#### 1.2.2 格式检测机制

**Binary 格式检测**:
```go
func tryFindProto(in []byte) ([]byte, bool) {
    i := bytes.Index(in, ProtoEncodingPrefix)
    if i >= 0 && i < len(in) {
        return in[i:], true
    }
    return nil, false
}
```

**JSON 格式检测**:
```go
func tryFindJson(in []byte) (*json.RawMessage, bool) {
    // 查找 { 或 [ 开头的有效 JSON
    i := bytes.IndexAny(in, jsonStartChars)
    for i >= 0 && i < len(in) {
        in = in[i:]
        if len(in) < 2 {
            break
        }
        err := json.Unmarshal(in, &js)
        if err == nil {
            return &js, true
        }
        in = in[1:]
        i = bytes.IndexAny(in, jsonStartChars)
    }
    return nil, false
}
```

#### 1.2.3 转换矩阵

| 输入格式 | 输出格式 | 处理方式 |
|----------|----------|----------|
| StorageBinary | Protobuf | 直接提取 runtime.Unknown.Raw |
| StorageBinary | JSON/YAML | DecodeUnknown → runtime.Decode → runtime.Encode |
| JSON | YAML | json.Unmarshal → yaml.Marshal |
| JSON | JSON | 直接返回（添加换行符） |
| Protobuf | StorageBinary | 不支持（报错） |

#### 1.2.4 Codec 创建

```go
func newCodec(codecs serializer.CodecFactory, typeMeta *runtime.TypeMeta, mediaType string) (runtime.Codec, error) {
    // StorageBinary 按 Protobuf 处理
    if mediaType == StorageBinaryMediaType {
        mediaType = ProtobufMediaType
    }

    // 获取序列化器信息
    mediaTypes := codecs.SupportedMediaTypes()
    info, ok := runtime.SerializerInfoForMediaType(mediaTypes, mediaType)

    // 解析 GroupVersion
    gv, err := schema.ParseGroupVersion(typeMeta.APIVersion)

    // 创建编解码器
    encoder := codecs.EncoderForVersion(info.Serializer, gv)
    decoder := codecs.DecoderToVersion(info.Serializer, gv)
    codec := codecs.CodecForVersions(encoder, decoder, gv, gv)

    return codec, nil
}
```

### 1.3 pkg/data 模块（数据访问层）

#### 1.3.1 核心数据结构

```go
// KeySummary 代表 etcd 中存储的 Kubernetes 对象
type KeySummary struct {
    Key      string               // etcd key
    Version  int64                // 版本号
    Value    any                  // 解码后的值
    TypeMeta *runtime.TypeMeta    // 类型元数据
    Stats    *KeySummaryStats     // 统计信息
}

// KeySummaryStats 统计信息
type KeySummaryStats struct {
    VersionCount         int   // 版本数量
    KeySize              int   // key 大小
    ValueSize            int   // value 大小
    AllVersionsKeySize   int   // 所有版本的 key 总大小
    AllVersionsValueSize int   // 所有版本的 value 总大小
}

// 查询投影（字段选择）
type KeySummaryProjection struct {
    HasKey   bool
    HasValue bool
}
```

#### 1.3.2 过滤器设计

**Filter 接口**:
```go
type Filter interface {
    Accept(ks *KeySummary) (bool, error)
}
```

**PrefixFilter 实现**:
```go
type PrefixFilter struct {
    prefix string
}

func (ff *PrefixFilter) Accept(ks *KeySummary) (bool, error) {
    return strings.HasPrefix(ks.Key, ff.prefix), nil
}
```

**FieldFilter 实现**:
```go
type FieldFilter struct {
    *FieldConstraint
    lhsTemplate *template.Template
}

func (ff *FieldFilter) Accept(ks *KeySummary) (bool, error) {
    // 使用 Go template 从 KeySummary 提取字段值
    buf := new(bytes.Buffer)
    err := ff.lhsTemplate.Execute(buf, ks)
    val := buf.String()

    // 执行比较操作
    switch ff.op {
    case Equals:
        return val == ff.rhs, nil
    }
}
```

#### 1.3.3 BoltDB 访问

**数据库打开**:
```go
func boltOpen(path string) (*bolt.DB, error) {
    // 检查文件存在
    if _, err := os.Stat(path); os.IsNotExist(err) {
        return nil, fmt.Errorf("file does not exist: %s", path)
    }

    // 只读模式打开
    return bolt.Open(path, 0o400, &bolt.Options{
        ReadOnly: true,
    })
}
```

**数据遍历**:
```go
func walk(db *bolt.DB, f func(r revKey, kv *mvccpb.KeyValue) (bool, error)) error {
    return db.View(func(tx *bolt.Tx) error {
        b := tx.Bucket(keyBucket)  // "key"
        c := b.Cursor()

        for k, v := c.First(); k != nil; k, v = c.Next() {
            revision := bytesToRev(k)
            kv := &mvccpb.KeyValue{}
            err := kv.Unmarshal(v)

            done, err := f(revision, kv)
            if done {
                break
            }
        }
        return nil
    })
}
```

#### 1.3.4 Revision 编码解析

```go
// revKey 表示 key-value 空间的修改
type revKey struct {
    main      int64  // 主修订版本
    sub       int64  // 子修订版本（同一批次中的顺序）
    tombstone bool   // 是否是墓碑标记（删除）
}

// Revision 字节布局:
// [0:8]   - main revision (大端序 int64)
// [8]     - '_' 分隔符
// [9:17]  - sub revision (大端序 int64)
// [17]    - 可选的墓碑标记 't'

func bytesToRev(bytes []byte) revKey {
    r := revKey{
        main: int64(binary.BigEndian.Uint64(bytes[0:8])),
        sub:  int64(binary.BigEndian.Uint64(bytes[9:])),
    }
    if len(bytes) >= markedRevBytesLen {
        r.tombstone = bytes[markedRevBytesLen-1] == 't'
    }
    return r
}
```

#### 1.3.5 校验和计算

```go
type Checksum struct {
    Hash            uint32
    Revision        int64
    CompactRevision int64
}

func HashByRevision(filename string, revision int64) (Checksum, error) {
    h := crc32.New(crc32.MakeTable(crc32.Castagnoli))
    h.Write(keyBucket)

    walkRevision(db, revision, func(r revKey, kv *mvccpb.KeyValue) (bool, error) {
        h.Write(kv.Key)
        h.Write(kv.Value)
        return false, nil
    })

    return Checksum{h.Sum32(), latestRevision, compactRevision}, nil
}
```

### 1.4 pkg/scheme 模块（类型系统）

#### 1.4.1 自动生成机制

`scheme.go` 是通过 `hack/gen_scheme.sh` 自动生成的，包含所有 Kubernetes API 类型的注册：

```go
// 由 gen_scheme.sh 生成，不要直接编辑
func AddToScheme(scheme *runtime.Scheme) {
    _ = admissionregistrationv1.AddToScheme(scheme)
    _ = appsv1.AddToScheme(scheme)
    _ = corev1.AddToScheme(scheme)
    // ... 所有 K8s API 版本
}
```

#### 1.4.2 CodecFactory 初始化

```go
import "k8s.io/apimachinery/pkg/runtime/serializer"

var (
    Scheme = runtime.NewScheme()
    Codecs = serializer.NewCodecFactory(Scheme)
)

func init() {
    AddToScheme(Scheme)
}
```

### 1.5 pkg/client 模块（etcd 客户端）

#### 1.5.1 接口设计

```go
// Client 定义 etcd 客户端操作
type Client interface {
    Get(ctx context.Context, prefix string, opOpts ...OpOption) (rev int64, err error)
}

// OpOption 操作选项模式
type OpOption func(*op)

func WithGroupResource(gr schema.GroupResource) OpOption
func WithName(name, namespace string) OpOption
func WithResponse(response func(kv *KeyValue) error) OpOption
func WithChunkSize(chunkSize int64) OpOption
```

#### 1.5.2 使用示例

```go
client := NewClient(etcdClient)
rev, err := client.Get(ctx, "/registry/pods",
    WithGroupResource(corev1.Resource("pods")),
    WithName("mypod", "default"),
    WithResponse(func(kv *KeyValue) error {
        // 处理每个 key-value
        return nil
    }),
)
```

## 2. 关键算法

### 2.1 格式检测算法

```
算法: DetectAndExtract
输入: 字节数组 in
输出: (媒体类型, 数据, 错误)

1. 尝试查找 Protobuf 前缀 (k8s\0)
   - 如果找到，返回 (StorageBinary, 从前缀开始的数据, nil)

2. 尝试查找 JSON
   - 查找第一个 { 或 [ 字符
   - 尝试从该位置解析 JSON
   - 如果成功，返回 (JsonMediaType, JSON 数据, nil)
   - 如果失败，继续查找下一个 { 或 [

3. 如果没有找到有效格式，返回错误
```

### 2.2 版本聚合算法

```
算法: ListKeySummaries
输入: BoltDB 文件, 过滤器, 投影, 修订版本
输出: KeySummary 列表

1. 打开 BoltDB 文件（只读模式）

2. 遍历 key bucket 中的所有条目:
   a. 如果指定了 revision，跳过更新的条目
   b. 解析 revision，判断是否墓碑标记
   c. 如果是墓碑，从 map 中删除 key
   d. 否则更新/添加到 map

3. 对于每个 key:
   a. 解码 value 为 JSON
   b. 应用过滤器
   c. 收集统计信息

4. 按 key 排序结果

5. 返回 KeySummary 列表
```

### 2.3 校验和计算算法

```
算法: HashByRevision
输入: BoltDB 文件, 修订版本
输出: Checksum

1. 打开 BoltDB 文件

2. 获取 compact revision

3. 初始化 CRC32 哈希（Castagnoli 多项式）

4. 写入 key bucket 名称到哈希

5. 遍历指定 revision 的所有 key-value:
   a. 对于每个非墓碑条目，写入 key 和 value

6. 返回哈希值、最新 revision、compact revision
```

## 3. 错误处理策略

### 3.1 错误类型

| 错误场景 | 处理方式 | 示例 |
|----------|----------|------|
| 文件不存在 | 返回包装错误 | `fmt.Errorf("file does not exist: %s", path)` |
| 格式检测失败 | 返回描述性错误 | `errors.New("does not appear to contain valid JSON or binary data")` |
| 解码失败 | 包装底层错误 | `fmt.Errorf("error decoding from %s: %w", inMediaType, err)` |
| 不支持的转换 | 返回明确错误 | `errors.New("unsupported conversion: protobuf to kubernetes binary...")` |

### 3.2 批处理模式错误处理

在批处理模式下，错误不会终止处理，而是输出到结果中：
```go
buf, _, err := encoding.Convert(...)
if err != nil {
    fmt.Fprintf(out, "ERROR:%v|\n", err)
} else {
    fmt.Fprintf(out, "OK|%s\n", string(buf))
}
```

## 4. 性能考虑

### 4.1 内存优化

1. **流式处理**: 批处理模式逐行读取，避免加载全部数据到内存
2. **延迟解码**: 只在需要时解码 value
3. **投影查询**: 只加载需要的字段

### 4.2 数据库访问优化

1. **只读模式**: 避免写入锁开销
2. **Cursor 遍历**: 使用 BoltDB Cursor 而非随机访问
3. **前缀过滤**: 优先使用前缀过滤减少遍历范围

## 5. 测试策略

### 5.1 单元测试

- 编解码函数测试
- 过滤器逻辑测试
- Revision 解析测试

### 5.2 集成测试

- 使用测试 BoltDB 文件 (`cmd/testdata/`)
- 端到端命令测试

### 5.3 测试数据

```
cmd/testdata/
├── boltDBFile.db      # 测试用 BoltDB 文件
└── ...
```

## 6. 代码生成

### 6.1 scheme.go 生成

```bash
# hack/gen_scheme.sh
# 1. 获取 k8s.io/api 包中的所有 API 组
# 2. 生成 import 语句
# 3. 生成 AddToScheme 函数调用
```

执行:
```bash
make generate
```

## 7. 附录

### 7.1 etcd 数据格式参考

**Key 格式**:
```
/registry/pods/<namespace>/<name>
/registry/services/<namespace>/<name>
/registry/configmaps/<namespace>/<name>
...
```

**Revision 格式**:
```
<main_rev>_<sub_rev>[t]
```

### 7.2 Kubernetes 存储编码

参考:
- `k8s.io/apimachinery/pkg/runtime/serializer/protobuf.go`
- `k8s.io/apiserver/pkg/storage/etcd3/store.go`
