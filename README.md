# OpenList Feishu Drive Driver

一个面向 [OpenList v4](https://github.com/OpenListTeam/OpenList) 的非官方飞书云盘驱动，使用飞书 OpenAPI 和用户 OAuth 挂载授权用户的**个人云空间**。

驱动使用 `user_access_token` 访问文件，支持通过授权码完成首次授权、自动刷新访问令牌，并将飞书轮换后的最新 `refresh_token` 持久化回 OpenList 数据库。无需给 OpenList 进程额外配置令牌环境变量。

> [!IMPORTANT]
> 本仓库目前是 OpenList 源码覆盖层，不是可在后台直接安装的二进制插件。使用前需要将驱动复制到 OpenList 源码并自行编译。

## 状态与兼容性

- 开发基线：OpenList `v4.2.6-14-g5447ecb0`
- OpenList 模块版本：`github.com/OpenListTeam/OpenList/v4`
- 认证模式：飞书用户 OAuth，仅面向授权用户的个人云空间
- 国内飞书和 Lark 均预留了 API/OAuth 地址配置
- 已完成单元测试覆盖：分页列表与对象映射、OAuth 授权码交换、令牌刷新与轮换持久化、在线文档导出链接、小文件上传和分片上传
- 已实机验证：国内飞书用户 OAuth、指定个人空间子目录挂载、目录浏览，以及在线文档导出下载

OpenList 或飞书 API 后续可能发生变化。集成到其他 OpenList 版本时，请先执行 `git apply --check` 和驱动测试。

## 已实现能力

- 分页列出文件夹内容
- 挂载整个个人云空间或指定的 `fld...` 子目录
- 下载普通文件，并向飞书下载接口传递鉴权请求头
- 20 MiB 及以下文件一次上传，较大文件按飞书返回的分片策略上传
- 同名普通文件通过 `file_token` 上传新版本
- 新建文件夹
- 移动文件或文件夹
- 复制普通文件
- 删除文件或文件夹
- 飞书在线文档导出：
  - 文档（`doc`/`docx`）→ DOCX
  - 电子表格（`sheet`）→ XLSX
  - 多维表格（`bitable`）→ XLSX
  - 幻灯片（`slides`）→ PPTX
- OAuth 授权码首次换取令牌
- `user_access_token` 内存缓存和提前刷新
- 鉴权失败后使缓存失效并重试一次
- `refresh_token` 自动轮换并持久化
- 授权码换取成功后自动清空一次性 `authorization_code`

## 已知限制

- 飞书 Drive 文件清单接口不返回普通文件大小，因此 OpenList 列表通常显示 `0 B`；刚上传文件的当前响应除外。
- Drive v1 没有适用于所有文件类型的统一重命名接口，驱动不声明重命名能力。
- 只支持复制普通文件，不支持复制整个文件夹。
- 思维笔记（`mindnote`）不在当前导出映射内，不会显示为可下载文件。
- 在线文档导出的内容格式正确，但 OpenList 当前仍使用飞书原始标题作为下载文件名，可能不会自动追加扩展名。下载后请按类型手动添加 `.docx`、`.xlsx` 或 `.pptx`。
- 上传 DOCX/XLSX/PPTX 只会创建普通文件，不会转换为飞书原生在线文档。
- 本项目只以个人云空间为目标，没有覆盖知识库、群空间或组织级共享盘场景。

## 仓库结构

```text
.
├── Dockerfile                 # 可复现的多阶段镜像构建
├── Dockerfile.acr             # ACR 从 GitHub 源码进行云端构建
├── docker-compose.yml         # 默认仅监听宿主机 127.0.0.1
├── drivers/feishu/            # 驱动实现和测试
├── integration/
│   └── drivers_all.patch      # 在 OpenList drivers/all.go 中注册驱动
├── scripts/
│   └── prepare-openlist-source.sh
└── README.md
```

## Docker 快速启动

为避免在 Docker 构建缓存中意外包含 OpenList 的数据库、配置或 OAuth 令牌，镜像只使用由 `git archive` 生成的干净上游源码快照。准备脚本会从 OpenList 官方前端仓库下载、校验并加入与后端匹配的前端资源，然后 Docker 注入飞书驱动、运行驱动测试并生成最终镜像。宿主机不需要安装 Go。

### 准备构建上下文

先准备一个 OpenList Git 源码目录。以下命令从指定 Git revision 导出已跟踪的源码，不会复制 `runtime-data`、`data.db` 或其他未跟踪文件：

```bash
./scripts/prepare-openlist-source.sh /path/to/OpenList HEAD
```

对 `HEAD`、分支或普通提交，脚本会自动使用官方 `edge` 前端；对精确的正式版本标签，脚本会使用同名正式前端。例如：

```bash
./scripts/prepare-openlist-source.sh /path/to/OpenList v4.2.6
```

准备结果位于被 `.gitignore` 排除的 `.docker/openlist-src`。前端压缩包会按 GitHub Release API 提供的 SHA-256 摘要进行验证。若后端含有初始化 API、前端却没有初始化向导代码，脚本会直接报错，防止生成版本不匹配的镜像。

如需显式指定前端发行标签，可使用第三个参数：

```bash
./scripts/prepare-openlist-source.sh /path/to/OpenList HEAD edge
```

只有在已经自行准备并确认 `public/dist` 与后端兼容时，才应使用 `local`：

```bash
./scripts/prepare-openlist-source.sh /path/to/OpenList HEAD local
```

访问 GitHub API 触发匿名限流时，可以通过专用变量提供 token；脚本不会读取可能属于其他工具的 `GITHUB_TOKEN`：

```bash
OPENLIST_GITHUB_TOKEN=github_pat_xxx \
  ./scripts/prepare-openlist-source.sh /path/to/OpenList HEAD
```

### 构建并启动

```bash
docker compose up -d --build
```

首次构建需要下载基础镜像和 Go 依赖，耗时取决于网络。查看状态和日志：

```bash
docker compose ps
docker compose logs -f openlist
```

Compose 在 Linux 上默认让**构建阶段**使用宿主机网络，以避免 Docker daemon 的 DNS 无法解析 Alpine/GitHub 域名；这不影响最终服务的运行网络。如果当前 Docker 平台不支持 host build network，可改用：

```bash
DOCKER_BUILD_NETWORK=default docker compose build
```

如果 `apk add` 同时把所有常见包报告为 `no such package`，并在前面出现 `DNS: transient error`，实际原因是软件仓库域名解析失败。Dockerfile 会自动重试五次；持续失败时应检查 Docker daemon 的 DNS 或代理配置。

Go 依赖默认通过 `https://goproxy.cn,direct` 下载，并使用 `sum.golang.google.cn` 校验；模块目录和编译缓存由 BuildKit 持久化。如果需要改用其他代理，可在 `.env` 中配置：

```dotenv
GO_MODULE_PROXY=https://proxy.golang.org,direct
GO_SUMDB=sum.golang.org
```

健康检查：

```bash
curl http://127.0.0.1:5244/ping
```

返回 `pong` 后访问：

```text
http://127.0.0.1:5244/
```

命名卷首次启动时没有管理员账号。打开 Web 页面并按初始化向导设置管理员用户名、密码和站点名称。管理员创建后，如需通过命令行修改密码可执行：

```bash
sudo docker compose exec openlist ./openlist admin set YOUR_PASSWORD
```

### 网络和数据持久化

Compose 默认发布端口：

```text
127.0.0.1:5244 -> container:5244
```

因此只有宿主机可以访问。需要允许局域网连接时，可在当前目录创建不会提交到 Git 的 `.env`：

```dotenv
OPENLIST_BIND=0.0.0.0
OPENLIST_PORT=5244
```

远程访问应在 OpenList 前配置 HTTPS 反向代理或通过可信 VPN，不要把普通 HTTP 和 WebDAV Basic Auth 直接暴露到公网。

运行数据保存在 Docker 命名卷 `openlist-feishu_openlist-data`，重新构建或删除容器不会丢失配置、数据库和轮换后的 `refresh_token`：

```bash
docker volume inspect openlist-feishu_openlist-data
```

停止服务但保留数据：

```bash
docker compose down
```

不要执行 `docker compose down -v`，除非确定要删除 OpenList 数据和飞书 OAuth 持久化信息。

### 自定义构建

使用其他兼容的 OpenList 标签、分支或提交时，重新准备源码快照：

```bash
./scripts/prepare-openlist-source.sh /path/to/OpenList OTHER_GIT_REF
docker compose build
docker compose up -d
```

准备脚本会自动为正式标签选择同名正式前端，为其他 revision 选择 `edge` 前端。不要把旧源码目录中残留的 `public/dist` 直接配给较新的后端，否则新实例可能只有登录页而没有初始化向导。

补丁如果不再适用于新版本，构建会在 `git apply --check` 阶段明确失败，而不会产出未注册飞书驱动的镜像。

直接构建和命名镜像：

```bash
docker build \
  --network=host \
  -t openlist-feishu:v4.2.6 .
```

发布到 GitHub Container Registry 的示例：

```bash
docker tag openlist-feishu:v4.2.6 ghcr.io/georgehu6/openlist-feishu:v4.2.6
docker login ghcr.io
docker push ghcr.io/georgehu6/openlist-feishu:v4.2.6
```

发布镜像时需要同时保留对应源码，并遵守 OpenList 的 AGPL-3.0 许可证。

## 阿里云 ACR 自动构建

仓库根目录的普通 `Dockerfile` 使用本机生成且不提交的 `.docker/openlist-src`，因此不能直接用于 ACR 的 GitHub 云端构建。ACR 构建规则应改用 `Dockerfile.acr`。该文件会在构建机内完成以下操作：

1. 拉取固定提交 `5447ecb07202c16b8d86d60c68266ac4e0053997` 的 OpenList 后端；
2. 下载官方 `edge` 前端并按 GitHub Release 提供的 SHA-256 校验；
3. 检查前端包含与后端匹配的初始化接口；
4. 注入飞书驱动、运行驱动测试并编译最终镜像。

在 ACR 控制台中使用以下构建规则：

| 配置项 | 值 |
| --- | --- |
| 类型 | Tag |
| Branch/Tag | `tags:release-v$version` |
| 构建上下文目录 | `/` |
| Dockerfile 文件名 | `Dockerfile.acr` |
| 镜像版本 | `$version` |
| 海外机器构建 | 开启 |
| 不使用缓存 | 关闭 |

如果控制台使用新版命名捕获组语法，可将 Branch/Tag 改为 `release-v(?<version>.*)`，镜像版本改为 `${version}`。正则构建规则只能由匹配的 Git tag push 自动触发，不能在控制台手动构建。

首次发布建议从 `v0.1.0` 开始。提交并推送代码后创建触发标签：

```bash
git tag -a release-v0.1.0 -m "Release v0.1.0"
git push origin release-v0.1.0
```

按照截图中的规则，ACR 最终生成的镜像 tag 为 `0.1.0`。如需同时生成 `latest`，可在同一构建规则中添加第二个固定镜像版本 `latest`；对于长期部署，仍建议固定使用版本 tag。

ACR 支持把 Dockerfile 中的 `ARG` 配置为构建参数。升级上游 OpenList 时应同时设置并测试以下参数：

| 构建参数 | 当前默认值 |
| --- | --- |
| `OPENLIST_REF` | `5447ecb07202c16b8d86d60c68266ac4e0053997` |
| `OPENLIST_VERSION` | `v4.2.6-14-g5447ecb0` |
| `FRONTEND_RELEASE` | `edge` |
| `GO_MODULE_PROXY` | `https://goproxy.cn,direct` |
| `GO_SUMDB` | `sum.golang.google.cn` |

不要通过普通 Docker build 参数传入 GitHub token、飞书 App Secret 或 OAuth token，因为构建参数和构建日志不适合存放密钥。飞书凭据只应在 OpenList 启动后通过管理页面写入持久化数据卷。

构建完成后，从 ACR 仓库“访问凭证”页面取得登录地址，从“镜像版本”页面复制完整镜像地址。典型的拉取和启动方式为：

```bash
sudo docker login REGISTRY_DOMAIN
sudo docker pull REGISTRY_DOMAIN/NAMESPACE/REPOSITORY:0.1.0
sudo docker run -d \
  --name openlist-feishu \
  --restart unless-stopped \
  -p 127.0.0.1:5244:5244 \
  -v openlist-data:/opt/openlist/data \
  REGISTRY_DOMAIN/NAMESPACE/REPOSITORY:0.1.0
```

其中 `REGISTRY_DOMAIN/NAMESPACE/REPOSITORY` 请替换为 ACR 控制台显示的实际仓库地址。

## 集成和编译

### 1. 准备 OpenList 源码

建议先切换到与本项目匹配的 OpenList 版本；如果使用更新版本，请自行处理可能出现的接口差异。

```bash
git clone https://github.com/OpenListTeam/OpenList.git
cd OpenList
git checkout v4.2.6
```

### 2. 复制并注册驱动

将 `/path/to/openlist-feishu-driver` 和 `/path/to/OpenList` 替换为本机实际路径：

```bash
cp -a /path/to/openlist-feishu-driver/drivers/feishu /path/to/OpenList/drivers/
cd /path/to/OpenList
git apply --check /path/to/openlist-feishu-driver/integration/drivers_all.patch
git apply /path/to/openlist-feishu-driver/integration/drivers_all.patch
gofmt -w drivers/feishu/*.go drivers/all.go
```

如果补丁因上游代码变化而无法应用，只需在 `drivers/all.go` 的匿名导入列表中加入：

```go
_ "github.com/OpenListTeam/OpenList/v4/drivers/feishu"
```

### 3. 准备 Go

以 OpenList 自己的 `go.mod` 为准。当前开发基线声明：

```text
go 1.26.0
toolchain go1.27.1
```

建议允许 Go 自动选择并下载 toolchain：

```bash
go env -w GOTOOLCHAIN=auto
go version
```

### 4. 测试并构建

先运行驱动测试：

```bash
go test ./drivers/feishu
```

推荐使用 OpenList 的构建脚本同时准备前端资源并生成 `bin/openlist`：

```bash
bash build.sh release docker
```

如果环境中存在失效的 `GITHUB_TOKEN`，GitHub API 可能返回 HTTP 401。可以先移除该变量后重试公开仓库下载：

```bash
unset GITHUB_TOKEN
bash build.sh release docker
```

构建脚本输出 `error: tag 'beta' not found` 通常只是尝试删除不存在的本地标签，不是构建失败原因。

如果已经准备好 `public/dist/index.html`，也可以只构建后端：

```bash
mkdir -p bin
go build -tags=jsoniter -o bin/openlist .
```

> [!WARNING]
> 运行目录中必须存在 `public/dist/index.html`。否则 OpenList 会以 `index.html not exist, you may forget to put dist of frontend to public/dist` 退出，Web 页面也不会监听在 5244 端口。

## 启动 OpenList

下面将运行数据保存在单独目录：

```bash
./bin/openlist --data /path/to/openlist-data server
```

首次运行会创建 `config.json` 和数据库。打开 Web 页面并按初始化向导创建管理员。管理员已经存在时，可以通过命令行修改密码：

```bash
./bin/openlist --data /path/to/openlist-data admin set YOUR_PASSWORD
```

默认访问地址：

```text
http://127.0.0.1:5244/
```

OpenList 默认可能监听 `0.0.0.0`。只允许本机访问时，停止服务，将数据目录中 `config.json` 的以下字段改为：

```json
"address": "127.0.0.1"
```

然后重新启动。不要为了多设备访问直接将未加密的 5244 端口暴露到公网；应使用 HTTPS 反向代理、可信内网或 VPN。

## 飞书应用配置

1. 登录[飞书开放平台](https://open.feishu.cn/app)，创建企业自建应用。
2. 在“权限管理”申请以下**用户身份权限**，并由管理员批准：
   - `drive:drive`：必需，访问和管理个人云空间文件。
   - `offline_access`：必需，签发并刷新 `refresh_token`。
   - `drive:export:readonly`：可选，仅在需要下载飞书在线文档时申请。
3. 也可以在“批量导入/导出权限”中导入：

   ```json
   {
     "scopes": {
       "tenant": [],
       "user": [
         "drive:drive",
         "offline_access",
         "drive:export:readonly"
       ]
     }
   }
   ```

4. 在“开发配置 → 安全设置 → 重定向 URL”中登记回调地址，例如：

   ```text
   http://127.0.0.1:53682/callback
   ```

5. 如果后台提供“刷新 user_access_token”开关，请启用。
6. 创建并发布应用版本。修改权限后需要重新发布版本才能在正式环境生效。
7. 从“凭证与基础信息”取得 App ID 和 App Secret。

本模式不需要启用机器人，也不需要把个人文件夹分享给机器人或群聊。

## 首次 OAuth 授权

先生成一个一次性 `state`：

```bash
openssl rand -hex 16
```

替换下列地址中的 `APP_ID`、经过 URL 编码的 `REDIRECT_URI` 和 `RANDOM_STATE`，然后在浏览器打开：

```text
https://accounts.feishu.cn/open-apis/authen/v1/authorize?client_id=APP_ID&response_type=code&redirect_uri=REDIRECT_URI&scope=drive%3Adrive%20offline_access%20drive%3Aexport%3Areadonly&state=RANDOM_STATE
```

授权后飞书会跳转到：

```text
REDIRECT_URI?code=ONE_TIME_CODE&state=RANDOM_STATE
```

本地没有运行回调服务时，浏览器显示无法连接是正常的。确认返回的 `state` 与发起授权时一致，然后从地址栏复制 `code` 参数。

授权码只能使用一次且有效期很短，不要提交到 GitHub、日志或截图中。

## OpenList 存储配置

登录 OpenList 管理后台，新增存储并选择 `FeishuDrive`：

| 字段 | 填写内容 |
| --- | --- |
| Mount path | OpenList 挂载路径，例如 `/feishu` |
| Root folder id | 可选；留空挂载个人云空间根目录，或填写目标子目录的 `fld...` token |
| App ID | 飞书自建应用 App ID |
| App Secret | 飞书自建应用 App Secret |
| Refresh token | 已有令牌时填写；首次使用授权码时留空 |
| Authorization code | 仅首次换取令牌时填写，成功后会自动清空 |
| Redirect URI | 必须与飞书后台登记及获取授权码时使用的地址完全一致 |
| OAuth token URL | 国内飞书：`https://accounts.feishu.cn/oauth/v3/token`；Lark：`https://accounts.larksuite.com/oauth/v3/token` |
| API base | 国内飞书：`https://open.feishu.cn/open-apis`；Lark：`https://open.larksuite.com/open-apis` |
| Order by | `EditedTime`（默认）或 `CreatedTime` |
| Order direction | `ASC` 或 `DESC` |
| Online document mode | `export` 导出支持的在线文档；`hide` 隐藏所有在线文档 |
| Export timeout | 等待在线文档导出的最长秒数，默认 30 |

首次保存时，驱动会使用 `authorization_code` 换取令牌，将 `refresh_token` 写入 OpenList 存储配置，并清空授权码。后续刷新及令牌轮换无需人工干预。

`Root folder id` 可以从个人空间文件夹 URL 中取得，例如 URL 中 `/drive/folder/fld...` 后的 `fld...` 部分。填写它后，OpenList 只显示该子目录及其内容。

飞书清单 API 的 `order_by` 只接受 `EditedTime` 和 `CreatedTime`。早期版本错误使用了 `Name`；当前驱动会自动迁移为 `EditedTime`。

### 下载代理

飞书下载接口要求 `Authorization` 请求头，因此必须经过 OpenList 代理。驱动已设置 `OnlyProxy: true`，不要将其改为 302 直链下载。

### 多实例注意事项

不要让多个正在运行的 OpenList 实例共用同一个 `refresh_token`。飞书可能在刷新时轮换令牌，一个实例保存新令牌后，其他实例仍持有旧值并可能失去授权。多实例部署应分别完成 OAuth 授权，并妥善保存各自的数据目录。

## 在线文档下载格式

如果浏览器下载的飞书在线文档没有扩展名，请按来源类型重命名：

| 飞书类型 | 导出格式 | 应添加的扩展名 |
| --- | --- | --- |
| 文档 | Microsoft Word | `.docx` |
| 电子表格 | Microsoft Excel | `.xlsx` |
| 多维表格 | Microsoft Excel | `.xlsx` |
| 幻灯片 | Microsoft PowerPoint | `.pptx` |

例如：

```bash
mv "飞书文档标题" "飞书文档标题.docx"
```

如果重命名后仍无法打开，可以使用 `file` 检查下载内容。若结果是 JSON 或纯文本，说明保存的可能是飞书错误响应，而不是 Office 文件。

## WebDAV 与 Zotero

OpenList 会在 `/dav/` 暴露 WebDAV。当前飞书驱动具备 Zotero 附件同步所需的列表、读取、上传、覆盖、建目录和删除能力，但本项目尚未声明完成多设备 Zotero 端到端验证。

推荐在 `/feishu` 下创建 `ZoteroWebDAV`，并创建一个专用 OpenList 用户：

- 基础路径：`/feishu/ZoteroWebDAV`
- 权限：创建目录或上传、删除、WebDAV 读取、WebDAV 管理

在同一台电脑的 Zotero 中配置：

| Zotero 字段 | 值 |
| --- | --- |
| 类型 | WebDAV |
| 协议 | `http` |
| 地址 | `127.0.0.1:5244/dav/` |
| 用户名/密码 | 专用 OpenList 用户的凭证 |

不要在地址末尾手动填写 `/zotero`；Zotero 会自动追加。最终文件会存放在 `/feishu/ZoteroWebDAV/zotero/`，并表现为成对的 `.zip` 和 `.prop` 文件，这是正常格式。

WebDAV 只同步 Zotero 个人文库的附件，不同步群组文库附件；条目、标签和笔记仍由 Zotero 数据同步处理。不要把 Zotero 数据库目录直接放入飞书或其他云盘同步目录。

当前 OpenList 如果只监听 `127.0.0.1`，其他电脑和移动设备无法连接。需要多设备使用时，应通过 HTTPS 暴露同一个 OpenList 实例，或在每台桌面设备上分别运行并分别授权 OpenList。Android Zotero 默认不接受普通 HTTP WebDAV。

迁移旧 WebDAV 时，先确保附件已全部下载到本机并保留旧服务器副本。切换地址后先正常同步；如果只有新附件上传，在备份 Zotero 数据目录后，仅使用“设置 → 同步 → 重置 → 重置文件同步历史”，不要选择“替换在线文库”。参考 [Zotero 同步文档](https://www.zotero.org/support/preferences/sync)。

## 验证建议

完成配置后建议按顺序验证：

1. 列出挂载根目录。
2. 新建子目录。
3. 上传并下载一个小文件。
4. 覆盖同名普通文件。
5. 上传大于 20 MiB 的文件。
6. 移动和复制普通文件。
7. 删除测试文件及目录。
8. 下载一份飞书文档并按 DOCX 打开。
9. 如需 WebDAV，执行 Zotero“验证服务器”并检查 `.zip`/`.prop` 文件。

发生飞书错误时，驱动会返回 HTTP 状态、飞书业务错误码和 `request_id`，可使用 `request_id` 在飞书开放平台请求日志或排查工具中定位。

## 从旧版驱动升级

旧的 `tenant_access_token` 配置无法直接用于当前用户 OAuth 版本。升级时需要：

1. 为应用增加用户身份权限 `drive:drive` 和 `offline_access`。
2. 重新发布飞书应用版本。
3. 完成一次用户 OAuth 授权。
4. 在 OpenList 存储中填写新的授权码或 `refresh_token`。

旧配置中的 `order_by: Name` 会在初始化时自动迁移为 `EditedTime`。

## 安全说明

- 不要提交 App Secret、授权码、访问令牌、刷新令牌、OpenList 数据库或运行数据目录。
- OpenList 存储配置和数据库应视为敏感数据并限制文件权限。
- HTTP Basic Auth 只适合本机回环地址或受信任网络；远程 WebDAV 必须使用 HTTPS。
- 发布日志或错误截图前，先移除 OAuth 参数、Cookie、Authorization 请求头和个人文件名。

## 免责声明

本项目不是飞书、Lark 或 OpenList 官方驱动。使用前请自行评估 API 配额、数据安全和兼容性，并先在测试目录验证写入、覆盖和删除行为。
