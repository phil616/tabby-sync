# tabby-config-sync

[English](README.md)

这是一个只实现 Tabby 桌面客户端配置同步协议的精简服务。它使用 Go 和
SQLite，不包含 tabby-web 的浏览器终端、OAuth、连接网关或版本分发功能。

协议分析见 [docs/research.md](docs/research.md)，架构与安全设计见
[docs/design.md](docs/design.md)。

## 公网部署

前置条件：

- 一个解析到服务器的域名；
- 公网可访问 TCP 80/443 端口；
- Docker 与 Docker Compose。

```bash
cp .env.example .env
sed -i 's/sync.example.com/sync.your-domain.example/' .env
docker compose build
docker compose run --rm app user create --name alice
docker compose up -d
```

创建用户时会输出一次 `tcs_...` 同步令牌，请立即保存到密码管理器。Caddy
会自动申请并续期 TLS 证书。

在 Tabby 的“设置 → 配置同步”中填写：

- 同步主机：`https://sync.your-domain.example`
- Secret sync token：刚生成的 `tcs_...` 令牌

连接成功后，先使用“Upload as a new config”完成第一次上传，确认同步方向无误
后再开启自动同步。

2026 年 5 月 7 日合并的 Tabby 安全修复已强制同步主机使用 HTTPS。配置内容
可以包含之后会被终端执行的命令，因此不能把明文 HTTP 部署在公网。

## 用户管理

```bash
docker compose run --rm app user list
docker compose run --rm app user rotate-token --name alice
docker compose run --rm app user disable --name alice
docker compose run --rm app user enable --name alice
```

轮换令牌会立即使旧令牌失效。禁用用户不会删除其配置。

## 本地质量检查

```bash
go vet ./...
go test ./...
go test -race ./...
docker compose config
docker build -t tabby-config-sync:local .
```

## 自动发版

推送符合语义化版本的标签后，GitHub Actions 会自动测试并创建 GitHub Release：

```bash
git tag -a v1.0.0 -m "v1.0.0"
git push origin v1.0.0
```

Release 包含 Linux、macOS、Windows 的 AMD64/ARM64 压缩包，以及统一的
`checksums.txt`。例如 `v1.1.0-rc.1` 会自动标记为预发布版本。

本地检查发版配置：

```bash
make release-check
make release-snapshot
```

完整配置项、备份方法、API 文档和安全边界请查看英文
[README](README.md)、[OpenAPI](api/openapi.yaml) 与
[SECURITY.md](SECURITY.md)。
