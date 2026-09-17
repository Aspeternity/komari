# Komari — Aspeternity 维护版

[English](./README.md) | [简体中文](./README_zh-cn.md)

Komari 是一款轻量级、自托管的服务器监控工具，通过 Web 面板展示状态，并由轻量级 Agent 采集数据。

> [!IMPORTANT]
> 本仓库是基于原 `komari-monitor/komari` 项目的独立维护 Fork，目前以官方 `1.5.0-fix1` 为基础继续维护。它不是原作者官方延续版本，也不代表原作者背书。

## 当前维护版本

- **当前版本：** `1.5.1-asp.1`
- **后端：** `Aspeternity/komari`
- **前端：** `Aspeternity/komari-web`
- **生产前端分支：** `stable`
- **Docker 镜像：** `ghcr.io/aspeternity/komari`
- **持久化数据目录：** `/app/data`

生产 Docker 构建会固定使用我们自己维护的 `komari-web:stable`，不再自动跟随上游前端变化。

## 维护版目前的改进

在尽量保持 Komari 数据库、Agent 协议和原有部署方式兼容的前提下，目前已经加入：

- `/admin` 与 `/terminal` 强制与第三方主题路由隔离；
- 阻止第三方主题的根作用域 Service Worker 接管后台路由；
- 自动清理旧主题遗留的 Service Worker；
- 修复切换主题后因旧 HTML 缓存造成的白屏/404；
- 固定使用自维护的 `komari-web:stable` 前端；
- 使用 `npm ci` 保证前端依赖构建可复现；
- 生产前端更新后自动触发 Komari Docker 重建。

详细变更见 [CHANGELOG.md](./CHANGELOG.md)，维护规则见 [MAINTENANCE.md](./MAINTENANCE.md)。

## 主要功能

- **实时监控**：秒级展示服务器状态。
- **轻量 Agent**：适合 VPS、家用服务器和 Home Lab。
- **自托管**：数据由自己掌控。
- **Web 面板**：支持状态、历史数据、终端与后台管理。
- **主题与插件**：保留扩展能力，并加强第三方主题与后台的隔离。

## Docker 部署

```yaml
services:
  komari:
    image: ghcr.io/aspeternity/komari:latest
    container_name: Komari
    restart: unless-stopped
    ports:
      - "25774:25774"
    volumes:
      - ./data:/app/data
```

更新或启动：

```bash
docker compose pull
docker compose up -d
```

如果希望固定版本，建议不要长期使用 `latest`，可以写成：

```yaml
image: ghcr.io/aspeternity/komari:1.5.1-asp.1
```

如果从官方 Komari 镜像迁移到本维护版，只需要把镜像地址换掉，原有 `/app/data` 挂载保持不变即可。

## 版本规则

维护版版本号采用：

```text
<兼容版本>-asp.<维护修订号>
```

例如：

```text
1.5.1-asp.1
```

普通 `main` Docker 构建以仓库根目录的 `VERSION` 文件为版本来源；GitHub Release 构建使用 Release Tag；Snapshot 继续使用独立的时间戳预发布版本。

## 开发流程

后端功能通过 feature/fix 分支开发并验证后合并到 `main`。前端在 `Aspeternity/komari-web:radix` 开发，验证通过后再同步到 `stable`。生产环境只读取 `stable`，避免开发中的前端变化直接影响 Docker 镜像。

## 上游与许可证

原项目：`komari-monitor/komari`

原前端：`komari-monitor/komari-web`

原项目采用 MIT License。本 Fork 保留原版权及许可证声明，并在此基础上独立维护，不提供任何形式的担保。

> [!WARNING]
> Komari 包含监控及远程控制能力，请仅部署在你拥有或已获得授权管理的系统上。
