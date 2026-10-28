# Shock Stock

[![Build](https://github.com/qeqeq1/shockstock-monitor/actions/workflows/build.yml/badge.svg)](https://github.com/qeqeq1/shockstock-monitor/actions/workflows/build.yml)
[![Release](https://github.com/qeqeq1/shockstock-monitor/actions/workflows/release.yml/badge.svg)](https://github.com/qeqeq1/shockstock-monitor/actions/workflows/release.yml)

一个用于监控 [Shock Hosting Dedicated Servers](https://shockhosting.com/dedicated) 补货的工具。
它是单二进制 Go 服务，内置 Web 管理台、SQLite、SMTP 邮件提醒和 Bearer Token API，可用于个人补货提醒或外部自动化调用。

## 功能

- 后台定时轮询 Shock Hosting 库存接口，默认 60 秒，最低 30 秒。
- 监控 68 个套餐，覆盖 17 个机房。
- 同一次补货事件最多发送 3 封：发现补货立即发送，10 分钟和 30 分钟后若仍有货各提醒一次。
- 库存重新回到 0 会取消剩余提醒；之后再次从 0 变为有货会立即开始新一轮通知。
- SMTP 支持 STARTTLS、SSL/TLS 和不加密连接。
- SQLite 保存当前库存、库存变化、检测记录、邮件记录、日志和登录会话。
- 外部 API 支持状态查询、库存筛选和手动触发检测。
- 附带 Dockerfile、Docker Compose 和 systemd 服务文件。

## 自动构建与发布

推送到 `main` 或提交 Pull Request 时，GitHub Actions 会运行格式检查、单元测试和 `go vet`。
推送 `v*` 标签时会自动构建 Linux `amd64`/`arm64` 和 Windows `amd64`/`arm64`，
同时发布可直接下载的二进制文件、`.exe` 和对应压缩包，并创建 GitHub Release。

```bash
git tag v1.0.0
git push origin v1.0.0
```

## 本地运行

要求 Go 1.23 或更高版本。

```bash
go mod download
go run .
```

打开 `http://localhost:8080`。

第一次启动时如果没有设置 `ADMIN_PASSWORD`，程序会生成一个随机管理密码并打印在终端：

```text
INITIAL ADMIN PASSWORD: xxxxxxxxxxxxxxxxxx
```

登录后请立即在“API 与系统”页面修改密码。数据库默认保存在 `./data/shockstock.db`。

也可以显式设置初始密码：

```bash
ADMIN_PASSWORD='replace-with-a-strong-password' go run .
```

`ADMIN_PASSWORD` 只在数据库尚未初始化时生效。已有数据库需要重置密码时，使用一次 `RESET_ADMIN_PASSWORD`，成功后从环境变量中移除。

## SMTP 设置

在“邮件通知”页面填写：

- SMTP 主机和端口
- 加密方式：`STARTTLS`、`SSL/TLS` 或“不加密”
- 用户名和密码
- 发件地址和收件人

保存后点击“发送测试邮件”验证。使用账号密码时不要选择“不加密”。

邮件发送失败时，套餐会保留“待通知”状态；下一次库存检测会继续尝试发送。取消该套餐监控会同时清除待通知状态。

## API

管理界面“API 与系统”页面可以查看或重新生成 Token。所有 `/api/v1/` 接口都需要以下任一请求头：

```http
Authorization: Bearer <token>
```

```http
X-API-Key: <token>
```

### 查询状态

```bash
curl -sS \
  -H "Authorization: Bearer $SHOCK_API_TOKEN" \
  http://localhost:8080/api/v1/status
```

### 查询库存

```bash
# 全部套餐
curl -sS \
  -H "Authorization: Bearer $SHOCK_API_TOKEN" \
  http://localhost:8080/api/v1/stock

# 东京的有货套餐
curl -sS \
  -H "Authorization: Bearer $SHOCK_API_TOKEN" \
  "http://localhost:8080/api/v1/stock?location=jp&status=available"
```

`status` 支持 `available`、`out`、`unknown`、`watched`。`location` 使用页面中的机房代码，例如 `ny`、`jp`、`de`。

### 单个套餐

```bash
curl -sS \
  -H "Authorization: Bearer $SHOCK_API_TOKEN" \
  http://localhost:8080/api/v1/stock/117
```

### 立即触发检测

```bash
curl -sS -X POST \
  -H "Authorization: Bearer $SHOCK_API_TOKEN" \
  http://localhost:8080/api/v1/check
```

接口默认不开放 CORS，适合服务端调用。健康检查不需要 Token：`GET /healthz`。

## Linux 二进制部署

构建 Linux amd64：

```bash
make build-linux
```

也可以直接构建：

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags="-s -w" -o shockstock .
```

部署：

```bash
sudo useradd --system --home /var/lib/shockstock --shell /usr/sbin/nologin shockstock
sudo install -d -o shockstock -g shockstock -m 0750 /var/lib/shockstock
sudo install -m 0755 dist/shockstock-linux-amd64 /usr/local/bin/shockstock
sudo install -m 0644 deploy/shockstock.service /etc/systemd/system/shockstock.service
sudo install -m 0600 deploy/shockstock.env.example /etc/shockstock.env
sudo systemctl daemon-reload
sudo systemctl enable --now shockstock
sudo journalctl -u shockstock -f
```

修改 `/etc/shockstock.env` 后重启：

```bash
sudo systemctl restart shockstock
```

生产环境建议通过 Nginx、Caddy 等反向代理提供 HTTPS，并只监听 `127.0.0.1:8080`。反代传递 `X-Forwarded-Proto: https` 后，登录 Cookie 会自动添加 `Secure`。

## Docker

```bash
docker compose up -d --build
```

默认把数据放在名为 `shockstock-data` 的 Docker Volume 中。首次使用前，通过环境变量设置初始密码：

```bash
ADMIN_PASSWORD='replace-with-a-strong-password' docker compose up -d --build
```

## 环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `LISTEN_ADDR` | `:8080` | HTTP 监听地址 |
| `AUTO_PORT` | `true` | 端口被占用时自动尝试后续端口 |
| `AUTO_PORT_ATTEMPTS` | `20` | 自动尝试的最大端口数量 |
| `DATA_DIR` | `./data` | SQLite 数据目录 |
| `ADMIN_PASSWORD` | 随机生成 | 首次初始化管理密码 |
| `RESET_ADMIN_PASSWORD` | 空 | 重置已有管理密码，使用后移除 |
| `API_TOKEN` | 随机生成 | 首次初始化 API Token |
| `STOCK_SOURCE_URL` | Shock Hosting 接口 | 库存数据源 |

例如设置 `LISTEN_ADDR=127.0.0.1:8080` 且 8080 已被占用，服务会依次尝试
8081、8082，直到找到空闲端口；实际监听地址会写入启动日志。

生产环境使用 Nginx 反向代理或 Docker 固定端口映射时，建议设置
`AUTO_PORT=false`，避免上游代理仍指向旧端口。systemd 示例默认关闭自动换端口。

## 数据与备份

SQLite 启用 WAL。备份前建议先停止服务，再复制 `DATA_DIR/shockstock.db`；如果服务不能停止，应使用 SQLite 的 `.backup` 命令生成一致性备份。

SMTP 密码和 API Token 保存在 SQLite 中，请把数据目录权限限制为服务用户可读写。

## 测试

```bash
go test ./...
go vet ./...
```
