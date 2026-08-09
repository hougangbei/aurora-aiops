# 资产盘点后端基础开发参考

> 实现基线：资产盘点计划 P1-T1 至 P1-T5。本文只描述当前代码已经具备的配置、存储、凭据加密、SSH 传输和采集能力。

## 用途与当前状态

`server/internal/assets` 为服务器资产盘点提供后端基础组件：SQLite 数据模型、加密凭据封装、主机密钥校验、受限 SSH 执行与上传，以及固定命令的软件和主机信息采集。模块保持免 Agent，目标 Linux 主机无需安装常驻进程。

当前代码还没有资产 `Service`、HTTP API、RBAC 路由、前端页面或后台定时调度，也没有把 `config.Asset`、`CredentialCipher`、`Repository`、`SSHTransport` 和 `Collector` 装配到进程入口。类型和 Repository 方法不是可访问的线上接口，`AURORA_AIOPS_ASSET_COLLECT_INTERVAL` 目前也不会自动触发采集。这些能力由后续任务实现。

## 架构与数据流

```text
AURORA_AIOPS_ASSET_* 环境变量
              |
              v
       config.AssetConfig
       |                         |
       | 32-byte key             | collect interval
       v                         v
CredentialCipher          [future Scheduler]
       ^                         |
       | encrypt/decrypt         |
       |                         v
+------------------------------------------------------------+
| Future Service / API / RBAC / UI boundary (not implemented) |
+------------------------------------------------------------+
       ^                         |
       | server + envelope       | Collect
       |                         v
Repository <---------------> Collector
   ^                             |
   |                             | fixed command + limit
   |                             v
   |                        SSHTransport
   |                             |
   |                             | SHA256 host key + auth
   |                             v
   |                       target Linux host
   |                             |
   |                             | bounded CommandResult
   |                             v
   +--- Snapshot + []SoftwareItem+
   |
   +--> asset_credentials / asset_servers / project_installations
   +--> asset_snapshots（成功快照）
   +--> asset_software_items（该快照的软件明细）
```

`Collector` 只依赖 `RemoteTransport`，测试可以用 fake transport，不必建立 SSH 连接。未来 Service 负责读取服务器与密文、解密凭据、执行主机密钥确认和采集，并把成功或失败结果交给 Repository。当前模块没有实现这段编排。

## 文件与模块职责

| 文件 | 当前职责 |
| --- | --- |
| `server/internal/config/config.go` | 解析资产主密钥和采集间隔，暴露 `AssetConfig`。 |
| `server/internal/config/legacy_compat.go` | 为配置读取提供兼容层；当前资产配置使用同一读取规则。 |
| `server/internal/store/migrate.go` | 在一个事务中创建五张资产相关表和快照索引。 |
| `server/internal/assets/model.go` | 定义服务器、凭据、快照、软件和输入模型。 |
| `server/internal/assets/errors.go` | 定义供后续 Service 和路由映射使用的 sentinel error；其中部分错误当前尚未被调用。 |
| `server/internal/assets/cipher.go` | 实现 AES-256-GCM 凭据加解密。 |
| `server/internal/assets/repository.go` | 实现服务器与凭据事务、成功快照保留、最新软件查询和失败状态记录。 |
| `server/internal/assets/ssh.go` | 实现主机密钥探测、认证、受限输出命令和受保护上传。 |
| `server/internal/assets/collector.go` | 定义固定命令、解析主机信息与软件、归一化和去重。 |
| `server/internal/assets/*_test.go` | 用临时 SQLite、fake transport 和进程内 loopback SSH server 固化上述契约。 |

## 配置参考

| 环境变量 | 格式与默认值 | 当前行为 |
| --- | --- | --- |
| `AURORA_AIOPS_ASSET_ENCRYPTION_KEY` | 标准 Base64；解码后必须恰好为 32 字节。默认空。 | 非空但无法解码或长度不是 32 时，`config.Load` 返回错误。空值会得到空 `[]byte`，`config.Load` 仍成功。 |
| `AURORA_AIOPS_ASSET_COLLECT_INTERVAL` | Go duration，默认 `15m`，必须大于 0。 | 无法解析、`0s` 或负值会使 `config.Load` 失败；当前没有 scheduler 消费该值。 |

配置层与未来业务层的边界必须分清：

- 当前配置层允许空主密钥。`NewAESGCMCredentialCipher` 则严格要求 32 字节，空 key 无法构造 cipher。
- 当前启动流程没有检查数据库中是否已有凭据，也没有实例化资产 Service，因此不能宣称“存在凭据但缺 key 时进程会拒绝启动”。
- 后续 Service 做新增服务器或轮换凭据时，应在主密钥为空时拒绝写入，并映射已声明的 `ErrEncryptionUnavailable`；这仍是后续任务，不是当前行为。
- 采集间隔只是一项已校验的配置。未来调度器如何并发、补跑或修改采集状态，不属于当前实现。

## 数据与存储参考

### 五张表

| 表 | 约束与关系 |
| --- | --- |
| `asset_credentials` | `id` 主键；`auth_type` 仅允许 `password` / `private_key`；保存 `nonce`、`ciphertext`、`key_version` 和时间。表内没有明文字段。 |
| `asset_servers` | `id` 主键，`name` 唯一；`ssh_port` 为 1–65535；`status` 仅允许 `pending` / `online` / `offline` / `error`；`credential_id` 非空并引用凭据。地址和端口组合不唯一。 |
| `asset_snapshots` | `id` 主键；`server_id` 引用服务器并 `ON DELETE CASCADE`；`payload` 保存 `Snapshot` JSON；`collected_at` 用于最新顺序和保留。 |
| `asset_software_items` | 引用快照并 `ON DELETE CASCADE`；主键为 `(snapshot_id, category, name, architecture)`。同一快照不能保存同一数据库身份的多个版本。 |
| `project_installations` | 引用服务器并 `ON DELETE CASCADE`；`(server_id, project_id)` 唯一。当前只有 schema，没有资产 Repository 方法或部署业务。 |

删除服务器会级联删除快照、软件和安装记录。Repository 随后仅在没有其他服务器引用时删除凭据。凭据外键本身没有 `ON DELETE CASCADE`，因此被引用的凭据不能先删除。

### 公开读取与凭据隔离

`GetServer` 和 `ListServers` 联结凭据表，但只填充 `CredentialAuthType` 和 `CredentialConfigured=true`。`Server` 不包含 nonce、ciphertext 或解密结果。只有单独的 `GetCredential` 返回 `StoredCredential`，供受信任的未来 Service 使用；不要把该方法的结果直接序列化给 API。

Repository 在写入前复制 nonce/ciphertext，在 `GetCredential` 返回前再次复制，防止调用方通过切片别名改写已保存或已读取的 envelope。

### 时间、快照和失败语义

- Repository 的时间列统一先转 UTC，再使用固定宽度 `2006-01-02T15:04:05.000000000Z07:00` 写入。读取接受 `time.RFC3339Nano`；可空时间以空字符串表示。
- 每次 `SaveCollection` 都插入一个新的成功快照和其软件明细，不更新既有成功快照。整个保存过程在一个事务内完成；软件主键冲突等错误会回滚快照与服务器摘要。
- 最新快照、最新软件、摘要更新和保留都使用 `(collected_at DESC, id DESC)`。相同采集时间由较大的 `id` 决胜，不依赖插入顺序。
- 迟到的旧快照仍作为历史成功结果保存，但只有排序后的权威最新快照能更新 `asset_servers` 摘要、`online` 状态、`last_seen_at` 和 `last_collected_at`。因此旧结果不能覆盖更新结果，也不能清除更新结果之后记录的失败状态。
- 每台服务器只保留排序后的最新 30 个成功快照；删除旧快照会级联删除其软件明细。
- `MarkCollectionFailure` 只更新 `status`、`status_message` 和 `updated_at`。最后一次成功的系统摘要、`last_collected_at`、快照和软件保持不变。

### 共享凭据更新

schema 允许多个服务器引用同一凭据，Repository 的更新规则是：

1. 不传 replacement 时保留原凭据。
2. replacement 使用新 ID 时先插入新凭据，再把当前服务器切换到新 ID。这是 copy-on-write；旧凭据仍被引用时保留，否则删除。
3. replacement 沿用原 ID 且只有一个引用时允许原位替换。
4. replacement 沿用原 ID 但引用数大于 1 时返回包装后的 `ErrInvalidInput`，服务器与共享凭据都不改变。
5. 删除服务器时，共享凭据保留到最后一个引用被删除。

## 公共 Go 契约

### 状态、认证和数据类型

```go
type ServerStatus string

const (
	ServerPending ServerStatus = "pending"
	ServerOnline  ServerStatus = "online"
	ServerOffline ServerStatus = "offline"
	ServerError   ServerStatus = "error"
)

type CredentialAuthType string

const (
	AuthPassword   CredentialAuthType = "password"
	AuthPrivateKey CredentialAuthType = "private_key"
)
```

| 类型 | 关键字段或用途 |
| --- | --- |
| `CredentialSecret` | `Password`、`PrivateKey`、`Passphrase` 明文，仅在受信任的内存调用链中传递。 |
| `CredentialEnvelope` | `Nonce []byte`、`Ciphertext []byte`、`KeyVersion int`。 |
| `StoredCredential` | 凭据 ID、认证类型、envelope 和创建/更新时间。 |
| `Server` | 连接信息、状态、最新摘要、时间和非敏感凭据元数据。 |
| `CreateServerInput` / `UpdateServerInput` | 为后续 Service 预留的输入类型；当前没有 Service 消费它们。 |
| `Snapshot` | OS、内核、架构、主机名、CPU、内存、磁盘、负载、运行时间和采集时间。 |
| `SoftwareItem` | `Category`、`Name`、`Version`、`Architecture`、`Source`、`Status`。 |
| `RemoteTarget` | 地址、端口、用户名和已确认的期望主机指纹。 |
| `CommandResult` | 分离的 stdout/stderr、退出码和共享输出预算是否截断。 |
| `HostKeyError` | 期望/实际指纹和 `Changed` 标记；`Unwrap` 到稳定 sentinel error。 |

### CredentialCipher

```go
type CredentialCipher interface {
	Encrypt(CredentialSecret) (CredentialEnvelope, error)
	Decrypt(CredentialEnvelope) (CredentialSecret, error)
}

func NewAESGCMCredentialCipher(key []byte) (CredentialCipher, error)
```

### Repository

服务器和凭据：

```go
func NewRepository(db *sql.DB) *Repository
func (r *Repository) CreateServer(ctx context.Context, server Server, credential StoredCredential) (Server, error)
func (r *Repository) UpdateServer(ctx context.Context, server Server, credential *StoredCredential) (Server, error)
func (r *Repository) GetServer(ctx context.Context, id string) (Server, error)
func (r *Repository) ListServers(ctx context.Context) ([]Server, error)
func (r *Repository) DeleteServer(ctx context.Context, id string) error
func (r *Repository) GetCredential(ctx context.Context, id string) (StoredCredential, error)
func (r *Repository) ConfirmHostKey(ctx context.Context, id, fingerprint string, now time.Time) error
```

采集结果：

```go
func (r *Repository) SaveCollection(ctx context.Context, server Server, snapshot Snapshot, software []SoftwareItem) error
func (r *Repository) MarkCollectionFailure(ctx context.Context, id string, status ServerStatus, message string, now time.Time) error
func (r *Repository) LatestSnapshot(ctx context.Context, serverID string) (Snapshot, error)
func (r *Repository) ListLatestSoftware(ctx context.Context, serverID string) ([]SoftwareItem, error)
```

### RemoteTransport 与 Collector

```go
type RemoteTransport interface {
	ProbeHostKey(context.Context, RemoteTarget) (string, error)
	Run(context.Context, RemoteTarget, CredentialSecret, string, int64) (CommandResult, error)
	Upload(context.Context, RemoteTarget, CredentialSecret, io.Reader, int64, string, fs.FileMode) error
}

func NewSSHTransport(dialTimeout time.Duration) *SSHTransport

type Collector struct { /* unexported fields */ }

func NewCollector(remote RemoteTransport, now func() time.Time) *Collector
func (c *Collector) Collect(ctx context.Context, server Server, secret CredentialSecret) (Snapshot, []SoftwareItem, error)
```

`RemoteTransport.Run` 是底层编程接口，确实接受命令字符串。当前没有 HTTP 或浏览器入口能调用它；`Collector` 只传入编译期定义的固定命令。后续 Service 不得把用户输入拼进该参数。

### Sentinel errors

当前包导出 `ErrNotFound`、`ErrNameConflict`、`ErrInvalidInput`、`ErrEncryptionUnavailable`、`ErrHostKeyUntrusted`、`ErrHostKeyChanged` 和 `ErrActiveTask`。调用方应使用 `errors.Is`，不要匹配错误字符串。`ErrEncryptionUnavailable` 和 `ErrActiveTask` 是后续 Service/部署任务预留项，当前基础层尚未返回它们。

## 凭据安全设计

- `NewAESGCMCredentialCipher` 只接受 32 字节 key，对应 AES-256。构造时复制调用方 key，之后调用方清零或修改原切片不会改变 cipher。
- `Encrypt` 把 `CredentialSecret` 序列化为 JSON，为每次加密生成新的 GCM nonce，并使用不可变附加认证数据 `aurora-aiops/asset-credential/v1`。
- envelope 的当前 `KeyVersion` 固定为 `1`。未知版本、nonce 长度错误、ciphertext 过短、认证失败或反序列化失败都会返回空 secret 和稳定的非敏感错误。
- GCM 同时保护机密性和完整性。修改 ciphertext、使用错误 key 或改变附加认证数据都会导致认证失败。
- cipher 和 Repository 的错误不格式化明文、key、nonce 或 ciphertext。SSH、collector 和后续 Service 仍需各自保持错误摘要安全；当前代码没有一个可替代所有边界检查的通用秘密脱敏器。

## SSH 信任、执行与上传

### 主机密钥与认证

- 指纹由 `ssh.FingerprintSHA256` 生成，格式为 OpenSSH 风格的 `SHA256:...`。
- 没有期望指纹时，`ProbeHostKey` 返回实际指纹和可 `errors.Is(err, ErrHostKeyUntrusted)` 的 `HostKeyError`。
- 指纹不匹配时返回期望值、实际值，并可匹配 `ErrHostKeyChanged`。`Run` 和 `Upload` 在没有已确认指纹时直接拒绝。
- 指纹匹配的 probe 通过内部 sentinel 在 host-key callback 中停止握手，因此在验证指纹后、认证发生前返回成功。loopback 测试断言认证尝试次数为 0。
- 认证支持密码、未加密私钥和带 passphrase 的加密私钥。`PrivateKey` 非空时优先使用私钥；两者都为空时返回 `ErrInvalidInput`。
- 拨号、握手、活动命令和上传连接都响应 `context` 取消或截止时间。`NewSSHTransport` 的非正 dial timeout 回退到 10 秒。

### 命令输出边界

`Run` 要求非空、无 NUL 的命令，并要求调用方给出 1 字节至 16 MiB 的 `outputLimit`。stdout 和 stderr 共享这一预算，不是各自拥有一份预算；超过预算的字节被丢弃并设置 `CommandResult.Truncated=true`。远端非零退出保留已收集输出和退出码，同时返回 SSH error。

这里的 16 MiB 是传输层硬上限。Collector 还为每条固定命令设置更小的业务上限，见下节。

### 上传边界

- 上传在连接远端前完整写入本地临时 spool，并将文件权限设为 `0600`。这样可先验证声明长度和 EOF，长度不匹配不会触发 SSH 或改动远端。
- source 可直接使用 `*bytes.Reader`、`*bytes.Buffer`、`*strings.Reader`。其他 source 必须实现 `io.ReadCloser`，以便 context 取消时关闭阻塞读取；普通的未知 `io.Reader` 会在读取前被拒绝。
- 声明大小必须为 0 至 1 GiB，实际内容必须恰好等长。连续 100 次零字节读取会以 `io.ErrNoProgress` 失败。
- destination 必须是已清理的绝对路径，只允许 `/tmp/aurora-aiops/` 或 `/opt/aurora-aiops/` 的词法子路径。路径不能含控制字符，mode 必须只有非零普通权限位。
- 远端命令对选定 root、destination parent 直到 root 的每个词法组件逐项检查：目录必须存在、不是 symlink、UID 为 0，且 group/other 不可写。解析后的 root 必须等于词法 root，解析后的 parent 必须仍在 root 内，destination 本身也不能是 symlink。全部通过后才执行 `install -m`。
- 远端 stderr 与 stdout 分离；失败诊断将控制字符替换为空格并限制到 4 KiB，再包装到错误中。它只做字符与大小清理，不应被误解为完整的秘密识别器。

上传把已预配的 trusted root 当作控制面信任边界：能写 root 或其中任一目录组件的非特权用户不能替换已检查路径；root 已被攻陷不在该模型内。因此这两个根目录必须由管理员预先创建并保持 root 所有、不可被 group/other 写入。当前没有浏览器 SSH、任意命令 API 或用户脚本路由。

## Collector 参考

### 固定命令与上限

| ID | 必需性 | 上限 | 内容 |
| --- | --- | ---: | --- |
| `base` | 必需 | 1 MiB | `/etc/os-release`、kernel、arch、hostname、CPU、内存、根盘、load1、uptime 的版本化固定协议。 |
| `packages_debian` | 可选 | 8 MiB | `dpkg-query` 已安装包。 |
| `packages_rpm` | 可选 | 8 MiB | `rpm -qa` 的 name、完整 EVR 和 arch。 |
| `packages_apk` | 可选 | 8 MiB | `apk info -v`。 |
| `services_systemd` | 可选 | 4 MiB | systemd service unit-file 状态。 |
| `versions` | 可选 | 1 MiB | container runtime、Kubernetes 工具和 Aurora AIOps 的固定版本探测。 |

每条命令以 `LC_ALL=C` 开始，并来自包内静态 map。服务器 ID、地址、用户名、指纹和凭据不会插值进命令。base 失败、非零退出、截断或协议不合法会使整次 `Collect` 返回安全错误且不返回部分结果。可选命令失败或截断则保留其他数据，并生成 `collector_warning`。

### 归一化和解析

- OS：`ubuntu` / `debian` → `debian`；`rhel` / `centos` / `rocky` / `almalinux` / `fedora` → `rhel`；`alpine` → `alpine`；未知 ID 只转小写，不运行包管理器命令。
- 架构：`x86_64` → `amd64`，`aarch64` → `arm64`，其他值转小写保留。
- dpkg：命令输出 `${binary:Package}`、version、arch 和 `${db:Status-Status}`。只保留 `installed`；`config-files`、`not-installed` 和中间状态被忽略。多架构包名的 `:arch` 后缀从 name 移除，architecture 字段保留身份差异。
- RPM：要求 `EPOCHNUM:VERSION-RELEASE` 完整 EVR。两个候选都来自 RPM 时使用 epoch、version、release 的 rpm 风格比较，支持 `~` 的预发布排序；当前明确不实现 RPM caret 特殊语义。
- APK：从右向左寻找 `-<version>-r<digits>`，避免把包名中的连字符或数字误当分隔点；architecture 使用 base probe 的归一化结果。
- systemd：只接受二或三列且名称以 `.service` 结尾的记录，忽略表头、Legend 和汇总行。
- versions：只接受固定 category/name 组合。category 为 `runtime`、`kubernetes` 或 `aurora`；允许的名称在代码中逐项列举。

正常软件 category 包括 `package`、`service`、`runtime`、`kubernetes`、`aurora`。采集器自身的降级记录使用 `collector_warning`；格式错误使用 `<command_id>_malformed` 名称，并把忽略条数与截断原因合并成一条有界 warning。

### 数据安全、去重和取消

- 原始记录必须是有效 UTF-8、非空、无 control / format / line-separator / paragraph-separator 字符。名称上限 256 字节，version、architecture、status 等上限 512 字节。
- 需要清理的字段将非法字符替换为空格，按 UTF-8 字节边界截断，再去掉两端空白。警告不包含远端 stderr、地址或凭据。
- 返回前按数据库身份 `(category, name, architecture)` 去重。并行安装版本无法进入当前 schema，所以保留语义上最大的版本；语义相等时使用完整字段的词法顺序确定唯一 winner，结果本身也稳定排序。
- context 在 base 前、每个可选命令前后都会检查。取消会停止后续序列，并丢弃已经形成的部分 `Snapshot` 和 `SoftwareItem`。

## 扩展规则

新增采集命令或 parser 时遵守以下顺序：

1. 先写一个会失败的测试，覆盖新 ID、固定命令、输出上限、合法记录、畸形记录、截断和取消。运行目标测试并确认 RED 来自缺少的新行为，而不是测试夹具错误。
2. 在 `collector.go` 增加稳定 command ID 和 `collectorCommands` 条目。命令必须完全静态并设置 `LC_ALL=C`；不得拼接服务器字段、凭据、API 参数或其他运行时输入。
3. 明确它是 required 还是 optional。required 失败必须让采集整体失败且不泄漏远端细节；optional 失败必须转成有界 `collector_warning`。
4. parser 只接受明确的机器可读格式。先用 `validCollectorRawField` 验证，再归一化；不完整的截断尾记录不能入库。
5. 在产生 `SoftwareItem` 前确认数据库身份兼容性。当前主键不包含 version 或 source；如新来源可能产生同一 `(category, name, architecture)` 的多行，要么实现确定性的 winner，要么先迁移 schema，不能依赖插入顺序。
6. 保持既有 category/name 身份稳定。改变身份会让同一软件在历史快照中表现为删除后新增，影响后续差异计算。
7. 实现后运行格式化、目标测试和三个相关 package 的完整验证：

   ```bash
   gofmt -w server/internal/assets/collector.go server/internal/assets/collector_test.go
   cd server
   go test ./internal/assets -run Collector -count=1
   go test ./internal/config ./internal/store ./internal/assets -count=1
   go vet ./internal/assets
   ```

## 已知边界与取舍

- 免 Agent SSH 减少目标机安装成本，但依赖网络可达性、SSH 配置和主机密钥确认，也没有 Agent 的持续本地观测能力。
- 当前模块没有任意 shell 输入的产品入口。底层 `RemoteTransport.Run` 仍是可执行任意字符串的内部 primitive，因此后续边界必须继续使用固定 catalog。
- 软件表每个 `(category, name, architecture)` 只能有一行。采集器选择版本较大的 winner，这是有意的有损表示，不能表达并存版本。
- 上传只允许两个 trusted root。目录必须预先配置，传输层不会创建目录，也不会放宽 root 所有权和写权限检查。
- `asset_snapshots.payload` 直接保存 `Snapshot` JSON，没有 envelope 或 schema-version 字段。旧 JSON 的兼容读取目前依赖 Go JSON 的字段兼容规则；未来破坏性变更应先引入版本化 envelope 和迁移策略。
- `project_installations`、`CreateServerInput`、`UpdateServerInput`、`ErrEncryptionUnavailable` 和 `ErrActiveTask` 已预留，但不能据此推断对应业务已实现。

## 测试参考

当前测试可按关注点运行：

```bash
cd server
go test ./internal/config -run 'Asset' -count=1
go test ./internal/store -run 'Asset' -count=1
go test ./internal/assets -run 'AESGCM|Repository' -count=1
go test ./internal/assets -run 'SSHTransport' -count=1
go test ./internal/assets -run 'Collector' -count=1
go test ./internal/config ./internal/store ./internal/assets -count=1
```

- config 测试证明 Base64 长度、空 key、`15m` 默认值和正 duration 校验。
- store/repository 测试使用临时 SQLite，证明约束、事务回滚、级联、公共读取隔离、共享凭据规则、最新排序、旧结果不覆盖、失败保留和 30 条保留策略。
- cipher 测试证明 round trip、随机 nonce、tamper/错误版本/畸形 envelope 拒绝、调用方 key 复制和错误不带测试 secret。
- SSH 测试使用进程内 `127.0.0.1` loopback SSH server，覆盖主机密钥、密码与私钥认证、退出码、取消、共享输出预算和上传字节流。上传路径安全测试的一部分是内存 guard model，不是对真实 Linux 文件系统的验收。
- collector 测试使用 fake `RemoteTransport`，证明固定命令、限制、parser、warning、去重和取消；它不执行目标主机命令。

这些测试不等于真实主机 acceptance。当前没有容器化 SSH 集成环境或受支持 Linux VM 的端到端采集结果，文档也不作此声明。

## 相关设计与计划

- [资产管理与项目部署设计](../superpowers/specs/2026-08-09-asset-management-project-deployment-design.md)
- [资产盘点与免 Agent SSH 实施计划](../superpowers/plans/2026-08-09-asset-inventory-ssh.md)
