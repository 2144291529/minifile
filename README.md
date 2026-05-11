# gossh

一个使用 Go 编写的浏览器匿名文件传输项目。

当前版本聚焦中转版 MVP，目标是先把“匿名房间 + 端到端加密 + 断点续传 + QUIC 中转”这条主链路跑通，再逐步扩展到 WebRTC P2P、自动回退和性能增强。

## 项目特性

- 匿名房间传文件
- 自定义房间号和房间密码
- 不填写时自动生成房间号和密码
- 浏览器端基于密码派生密钥的端到端加密
- 发送文件页 / 下载文件页双页面切换
- 分享链接进入房间
- 上传下载百分比显示
- 上传下载暂停 / 继续
- 删除文件
- 断点续传
- HTTPS + HTTP/3 + QUIC 中转
- WebSocket 房间状态同步
- 本地磁盘存储
- 预留 S3 兼容对象存储支持

## 这个项目是干什么的

这个项目的目标，是做一个可自部署、匿名、尽量快、后续可扩展到 P2P 的浏览器文件传输工具。

它适合这些场景：

- 临时文件分享
- 团队内部传文件
- 局域网或公网自建传输页
- 想做“类似文叔叔，但可控、可二开”的方案

更多说明见：

- [开放说明](./OPEN_SOURCE.md)
- [更新说明](./CHANGELOG.md)

## 浏览器里怎么用

### 发送文件

1. 打开发送文件页面
2. 输入昵称
3. 可选输入房间号和房间密码
4. 如果不填，系统会自动生成
5. 点击“创建发送房间”
6. 选择文件后点击“发送文件”
7. 把分享链接发给对方

### 下载文件

1. 打开下载文件页面
2. 粘贴别人发来的分享链接
3. 或手动输入房间号和房间密码
4. 进入房间后查看当前文件列表
5. 点击下载按钮开始下载
6. 支持暂停、继续和删除文件

## 各平台怎么用

### Windows

如果你已经有编译好的 `gossh.exe`：

```powershell
.\gossh.exe
```

默认会启动：

- HTTP: `http://127.0.0.1:8080`
- HTTPS / HTTP3: `https://127.0.0.1:8443`

浏览器建议直接打开：

```text
https://127.0.0.1:8443
```

首次启动会自动生成自签名证书和运行数据目录。

### Linux

如果你已经有编译好的 `gossh_linux_amd64`：

```bash
chmod +x ./gossh_linux_amd64
./gossh_linux_amd64
```

后台运行可以这样：

```bash
nohup ./gossh_linux_amd64 > gossh.log 2>&1 &
```

### macOS

当前仓库默认提供的是源码构建方式，如果你本机有 Go 环境，可以直接运行：

```bash
go run ./cmd/gossh
```

如果要编译本机版本：

```bash
go build -o gossh ./cmd/gossh
./gossh
```

### 自己编译

#### 本机直接运行

```powershell
go run ./cmd/gossh
```

#### 编译 Windows

```powershell
$env:GOCACHE="D:\code代码插件\gossh\.gocache"
$env:GOMODCACHE="D:\code代码插件\gossh\.gomodcache"
go build -o gossh.exe ./cmd/gossh
```

#### 编译 Linux amd64

```powershell
$env:GOCACHE="D:\code代码插件\gossh\.gocache"
$env:GOMODCACHE="D:\code代码插件\gossh\.gomodcache"
$env:CGO_ENABLED="0"
$env:GOOS="linux"
$env:GOARCH="amd64"
go build -trimpath -ldflags="-s -w" -o gossh_linux_amd64 ./cmd/gossh
```

## 环境变量

### 服务地址

- `GOSSH_BASE_URL`
- `GOSSH_PLAIN_ADDR`
- `GOSSH_ADDR`

### 数据目录

- `GOSSH_DATA_DIR`
- `GOSSH_DB_PATH`
- `GOSSH_LOCAL_STORAGE_DIR`

### 存储后端

- `GOSSH_STORAGE_BACKEND`：`local` 或 `s3`
- `GOSSH_S3_ENDPOINT`
- `GOSSH_S3_BUCKET`
- `GOSSH_S3_REGION`
- `GOSSH_S3_ACCESS_KEY`
- `GOSSH_S3_SECRET_KEY`
- `GOSSH_S3_USE_SSL`

### TLS

- `GOSSH_TLS_CERT`
- `GOSSH_TLS_KEY`

### 传输参数

- `GOSSH_ROOM_TTL`
- `GOSSH_MAX_UPLOAD_SIZE`
- `GOSSH_DEFAULT_CHUNK_SIZE`
- `GOSSH_WS_BUFFER_SIZE`

### WebRTC 预留配置

- `GOSSH_WEBRTC_ENABLED`
- `GOSSH_STUN_SERVERS`
- `GOSSH_TURN_SERVERS`
- `GOSSH_WEBRTC_FALLBACK`

## GitHub Release 发布

这个仓库已经补了 GitHub Actions Release Workflow：

- 文件位置：`.github/workflows/release.yml`
- 触发方式：推送 `v*` 标签

也就是说：

- `push main` 只会推源码
- `push v0.1.0` 这类 tag 才会触发 Release 构建

## 当前版本定位

当前版本是中转版 MVP，不是最终形态。

重点先放在：

- 匿名房间主流程
- 文件上传下载主流程
- 端到端加密基础能力
- 断点续传
- 暂停继续
- 分享链接进入房间

## 未来计划

- WebRTC P2P 直连传输
- STUN / TURN 自动打洞和自动回退
- 中转与 P2P 自动链路选择
- 多通道分块传输
- 自适应窗口和并发优化
- 秒传与去重
- Worker 并行处理
- 限速、限流和反滥用
- 更完整的房间状态与文件展示
- 更完善的对象存储接入
