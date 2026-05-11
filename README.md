# gossh

匿名文件传输原型，使用 Go 构建，当前聚焦中转版 MVP：

- 浏览器端端到端加密，密钥保存在 URL fragment，不上传服务器
- 匿名房间
- 断点续传
- HTTPS + HTTP/3 双栈服务
- WebSocket 房间事件
- 本地磁盘 / S3 兼容对象存储抽象

## 运行

```powershell
go run ./cmd/gossh
```

默认监听 `https://127.0.0.1:8443`，首次启动会自动生成自签名证书和 SQLite 数据库。

## 环境变量

- `GOSSH_ADDR`: 监听地址，默认 `:8443`
- `GOSSH_DATA_DIR`: 数据目录，默认 `./data`
- `GOSSH_STORAGE_BACKEND`: `local` 或 `s3`
- `GOSSH_LOCAL_STORAGE_DIR`: 本地对象目录
- `GOSSH_S3_ENDPOINT`: S3 兼容端点
- `GOSSH_S3_BUCKET`: S3 bucket
- `GOSSH_S3_ACCESS_KEY`: access key
- `GOSSH_S3_SECRET_KEY`: secret key
- `GOSSH_S3_REGION`: region
- `GOSSH_S3_USE_SSL`: 是否启用 TLS

## 下一步

- 接入 `pion/webrtc` 做浏览器 P2P
- STUN/TURN 自动回退
- 多通道分块与自适应窗口
