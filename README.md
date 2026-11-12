# camoufox

`github.com/go-sdk/camoufox` 提供 Camoufox 远程 Firefox 连接、独立会话及登录快照管理。页面操作直接使用原生 Playwright API。

`New` 仅保存配置，`Connect` 启动预先安装的本地驱动并建立远程连接。SDK 的职责和业务侧约束见 [支持边界](#支持边界)，仓库结构见 [项目地图](PROJECT_MAP.md)。

## 环境要求与安装

```bash
go get github.com/go-sdk/camoufox
```

| 组件             | 版本或要求                                                   |
|------------------|--------------------------------------------------------------|
| 运行平台         | 仅支持 `linux/amd64`、`linux/arm64`                          |
| Go               | `1.27.0` 或更新版本                                          |
| 基础库           | `github.com/go-sdk/core v1.6.8`，使用 `errx` 和 `codec/json` |
| Go Playwright 包 | `github.com/mxschmitt/playwright-go v0.6201.1`               |
| 客户端驱动       | Playwright `1.62.1`                                          |
| 远程协议         | Playwright `1.62.x`，主、次版本必须一致                      |
| 参考镜像         | `starudream/camoufox:1.0.1`，服务端 Playwright `1.62.0`      |

参考镜像的配置见 [镜像说明](https://github.com/starudream/docker-image/tree/master/camoufox)。上表是源码中的版本契约，连接及浏览器行为需在部署环境验证。

本仓库在 `go.mod` 中通过 `replace` 固定 Playwright Go 的实际源码版本，使 `go get -u -v all` 可以更新其他依赖而不改变驱动协议。该规则仅在本仓库作为主模块时生效。下游应用由自己的模块依赖图选定 Playwright Go 版本；若需要固定到表中的版本，应在自己的主模块中添加相同的 `replace`，并使用配套驱动。

集合依赖沿用 Playwright 的 `golang-set/v2`，当前使用 `v2.8.0`，通过 `exclude` 排除引入 MongoDB 驱动的 `v2.9.0`，不替换为 v3。该规则只在本模块作为主模块时生效；下游应用若执行依赖升级，需在自己的 `go.mod` 中配置相应排除规则。未来 v2 版本仍需重新检查依赖，Playwright 升级还需核对远程协议版本。

## 提前准备驱动

客户端需要配套驱动和 Node.js，无需 Python 或本地浏览器。在部署准备阶段显式执行：

```bash
PLAYWRIGHT_DRIVER_PATH=/opt/camoufox-driver \
  go run github.com/mxschmitt/playwright-go/cmd/playwright@v0.6201.1 --version
```

该上游命令准备驱动和 Node.js，并显示驱动版本；不会安装浏览器。不要追加 `install`，否则可能下载本地浏览器。也可以在独立的部署程序中调用 `playwright.Install(&playwright.RunOptions{DriverDirectory: directory, SkipInstallBrowsers: true})`。

生产或离线环境应在相同目标 Linux 架构上准备驱动，连同业务程序交付。默认驱动目录包含 `package/` 和 `node`。如果准备阶段设置了 `PLAYWRIGHT_NODEJS_PATH`，还需交付该外部 Node.js，并在运行时设置相同用途的路径。驱动目录与登录快照目录相互独立。

| 配置                        | 说明                                                |
|-----------------------------|-----------------------------------------------------|
| `WithDriverDirectory(path)` | 显式驱动目录，优先于环境变量                        |
| `PLAYWRIGHT_DRIVER_PATH`    | 未指定目录时由上游读取                              |
| `PLAYWRIGHT_NODEJS_PATH`    | 使用预装 Node.js                                    |
| `PLAYWRIGHT_CLI_PATH`       | 使用外部管理的 `cli.js`，必须与 Go 包的驱动版本一致 |

未指定驱动目录时使用上游 Linux 缓存目录 `~/.cache/ms-playwright-go/1.62.1`。

## 首次连接与手动登录

以下函数创建一个可在 KasmVNC 中操作的页面，业务确认登录后按回车保存快照。调用方传入真实业务登录地址，自己判断登录是否成功。

```go
package example

import (
	"bufio"
	"fmt"
	"os"

	"github.com/go-sdk/camoufox"
	"github.com/go-sdk/core/errx"
	"github.com/mxschmitt/playwright-go"
)

func login(endpoint, loginURL, statePath string) (result error) {
	client := camoufox.New(camoufox.WithEndpoint(endpoint))
	defer func() {
		result = errx.Join(result, client.Close())
	}()
	if err := client.Connect(); err != nil {
		return err
	}
	session, err := client.NewSession(playwright.BrowserNewContextOptions{
		NoViewport: playwright.Bool(true),
	})
	if err != nil {
		return err
	}
	page, err := session.Context().NewPage()
	if err != nil {
		return err
	}
	if _, err := page.Goto(loginURL); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(os.Stdout, "请在 KasmVNC 中完成并确认登录，然后按回车保存快照。"); err != nil {
		return err
	}
	if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		return err
	}
	return session.SaveState(statePath, playwright.BrowserContextStorageStateOptions{
		IndexedDB: playwright.Bool(true),
	})
}
```

参考镜像默认 Playwright 地址为 `ws://127.0.0.1:3000/ws`，KasmVNC 地址为 `https://127.0.0.1:8443`。在 KasmVNC 中操作上述会话创建的页面；关闭会话会关闭这些页面。KasmVNC 的认证不保护独立的 Playwright 端口。

## 恢复快照与代理

在已经连接的 `client` 上创建会话，`StorageStatePath` 指向客户端机器上的文件，浏览器容器无需挂载该文件。代理地址须从浏览器容器可达，密码等参数由调用方安全提供。

```go
session, err := client.NewSession(playwright.BrowserNewContextOptions{
	NoViewport:       playwright.Bool(true),
	StorageStatePath: playwright.String(".state/login.json"),
	Proxy: &playwright.Proxy{
		Server:   proxyServer,
		Username: playwright.String(proxyUsername),
		Password: playwright.String(proxyPassword),
	},
})
if err != nil {
	return err
}
page, err := session.Context().NewPage()
if err != nil {
	return err
}
// 使用 page 的原生 Playwright API 执行业务操作。
_ = page
```

无需代理时省略 `Proxy`；首次登录时省略 `StorageStatePath`。未指定代理的会话不继承其他会话的代理。SDK 保留 Playwright 的默认上下文选项，不自动开启 `NoViewport` 或忽略 HTTPS 错误。

快照文件不存在、JSON 损坏或恢复失败都会返回错误，不静默创建未登录会话。不要同时指定 `StorageState` 和 `StorageStatePath`。内联 `StorageState` 仍使用上游 Go 类型，需要完整 IndexedDB 时使用文件路径。

代理只影响上下文网络请求，不会重新生成浏览器启动阶段的指纹、GeoIP 或 WebRTC 信息；出口一致性需要业务独立设计和验证。

## 登录快照

```go
err := session.SaveState(".state/login.json", playwright.BrowserContextStorageStateOptions{
	IndexedDB: playwright.Bool(true),
})
```

- `SaveState(path)` 默认保存 Cookie 和 Local Storage；IndexedDB 需显式启用。
- 可选参数最多一个，其 `Path` 必须留空，目标路径以第一个参数为准。
- 保存保留上游写出的原始 JSON。恢复只读取并校验一次源文件，创建上下文后通过 `0600` 临时文件将已校验的内容交给原生 `SetStorageState`，使用后删除临时文件。快照校验使用 `core/codec/json`，不经过上游 Go 状态结构体，避免丢失 IndexedDB。
- 新建父目录使用 `0700`，同目录临时文件使用 `0600`。写入、格式校验和文件同步成功后重命名替换目标；替换前失败保留旧快照，并尝试清理临时文件。
- Linux 同目录重命名提供原子替换；这不是文件系统断电恢复保证。
- 已有目录权限由调用方管理。不同 Session 或进程写同一路径时，调用方应串行协调；SDK 仅串行化同一 Session 的保存。
- 不在关闭、进程退出或断线时隐式保存，业务负责保存时机和过期判断。

快照包含登录凭证，应放在受控持久化目录；本仓库忽略 `.state/`。原生选项 `Credentials` 还可包含虚拟 WebAuthn 凭证，调用方应按凭证文件管理。StorageState 的范围见 [支持边界](#支持边界)。

## Client 配置

```go
client := camoufox.New(camoufox.WithEndpoint(endpoint), camoufox.WithConnectTimeout(30*time.Second), camoufox.WithHeaders(headers), camoufox.WithDriverDirectory(driverDirectory))
```

- `WithEndpoint` 默认使用 `ws://127.0.0.1:3000/ws`，接受完整 `ws://`、`wss://` 地址，保留反向代理子路径和查询参数，不拼接 `/ws`；不接受 URL 用户信息或片段。
- `WithConnectTimeout` 默认 30 秒，零表示不限时，负数返回 `ErrInvalidOptions`。超时覆盖远程连接与协议初始化，不包含本地驱动启动；页面操作超时由原生 Playwright 选项管理。
- `WithHeaders` 在创建客户端时复制握手 Header，与页面请求 Header 独立，认证值由调用方安全提供。
- `WithDriverDirectory` 指定已准备好的驱动目录，未指定时遵循上游环境变量和缓存路径。

使用期间不要重新应用 Options 或并发修改配置。`IsConnected` 检查本地驱动进程及当前连接状态，不发起网络探测。

## 生命周期

`Session.Close` 关闭自己的上下文和页面。`Client.Close` 清理所管理的会话、断开本客户端远程连接并停止本地驱动，浏览器服务由容器管理。未显式发起断连时，清理客户端或失效连接会先等待正在关闭的会话并合并错误；检测到本地驱动退出时停止等待。直接并发重复调用 `Client.Close` 或 `Session.Close` 不会等待首次调用，应以首次调用返回作为收尾完成依据。

同一客户端并发 `Connect` 时，后来的调用返回 `ErrConnecting`。`Close` 会阻止正在创建的连接或会话向调用方发布，并等待并发 `Connect` 完成，合并其资源清理错误；普通连接失败本身不作为关闭错误。与 `NewSession` 并发时，仍需等待该调用返回，确认其失败清理已完成。原生 Page、Context 的并发和事件行为遵循上游接口，不应在事件回调中同步调用客户端生命周期方法，应投递到业务 goroutine 处理。

断线或容器重启后，旧 Session、Context 和 Page 失效。未关闭的 Client 可以显式再次 `Connect()`，清理旧连接，再用 `NewSession` 恢复快照、重新创建页面并验证登录。已调用 `Client.Close()` 时需要重新 `New()`。SDK 不自动重连或重放页面操作，业务自行决定重试，避免重复提交。

`Client.Disconnect(ctx)` 请求本地 Playwright 驱动断开远程连接，影响该连接上的所有会话。发出请求后不再接受新会话；并发调用共享正在执行的请求，失败后可重试。上下文只限制本次等待，不取消已发出的请求。只有确认远程连接断开才返回成功；若驱动先退出而无法确认断连，返回 `ErrDisconnect` 和 `ErrNotConnected`。断连成功也不代表远端已执行的操作被回滚。

发起断连后，`Connect` 或 `Close` 会接管旧驱动的清理，无需等调用方再次执行 `Disconnect`。断连失败或等待断连结果超过 2 秒时，SDK 仅在 Linux 提供绑定进程身份的 pidfd 时强制终止旧驱动。无法安全终止时返回 `ErrDriverStop`，后台继续等待正常清理，不使用裸 PID 强杀。强制终止可以回收本地驱动，不能证明旧页面调用已经返回。

本地驱动退出后，SDK 的 `IsConnected` 返回 false，`NewSession` 返回 `ErrNotConnected`，旧会话的 `SaveState` 返回 `ErrSessionClosed`；显式 `Connect` 会重新启动预装驱动。SDK 同步进程存活查询与独立查询句柄的释放，不另行等待或回收上游子进程。此检查与后续调用之间仍存在进程退出的时间窗口。

当前 Playwright Go 版本未在本地驱动退出时完整关闭内层远程协议连接，因此原生 `Browser.IsConnected`、Page 的关闭状态和事件可能仍保留旧值，已经挂起的原生 Page、Context 调用也可能无法返回；SDK 没有公开的上游接口可以解除这些等待。显式重连仅恢复后续新对象的使用，不保证旧调用结束；对故障后必须完整回收的任务，应在独立工作进程中运行，由业务监督进程重启。这一上游限制仍需修复。

## 错误处理

```go
if errx.Is(err, camoufox.ErrNotConnected) {
	// 由业务决定何时重连并重建会话。
}
if errx.Is(err, playwright.ErrTimeout) {
	// 上游超时类别被安全保留。
}
```

SDK 使用 `core/errx` 创建、合并和判断错误，也兼容标准库 `errors.Is`，完整类别见 `error.go`。上游错误可能包含端点、Header、代理密码或快照内容，SDK 只返回固定操作类别，并保留 `playwright.ErrTimeout`、`playwright.ErrTargetClosed` 类别，不透出上游错误正文。`Session.Close` 将已关闭远程对象视为清理成功。

调用原生 Page、Context 方法取得的错误由调用方管理。SDK 不启用上游调试日志；调用方设置 `DEBUG` 等调试环境变量时，上游可能输出协议和请求内容，应自行控制日志范围。

## 支持边界

- 仅支持远程 Firefox Playwright 连接，页面操作直接使用原生 Context、Page 和选项类型。
- SDK 管理客户端连接、独立会话和登录快照；容器负责浏览器进程、指纹、KasmVNC 和 Playwright 服务。
- 不启动本地浏览器，不自动下载安装依赖，不管理容器或网站业务流程。
- `StorageState` 不包含 Session Storage、完整用户目录、扩展、页面或完整浏览器配置，网站是否能恢复登录需要实际验证。
- 业务负责登录成功判断、快照保存时机和过期判断、代理出口一致性及重试策略。

## 开发

以下命令在支持的 Linux 环境中执行；其他系统可显式设置 `GOOS=linux`、`GOARCH=amd64` 或 `arm64` 做交叉编译和静态检查，无法直接运行生成的 Linux 程序。

```bash
make fmt
make lint
make build
git diff --check
```

`make lint` 先整理模块依赖，再检查包括 `integration` 标签在内的源码；`make build` 仅编译生产包。以上命令不执行测试、不启动驱动、不连接浏览器。空仓库尚未提交时，可设置 `GOFLAGS=-buildvcs=false` 执行静态检查与编译。

## 测试流程

[GitHub Actions](.github/workflows/golang.yml) 在推送到 `master` 或推送 `v*` 标签时运行，检查模块文件和源码，并通过 `make test` 执行普通单元测试。标签的检查和测试通过后，工作流创建 GitHub Release 并预热 Go 模块代理。双架构的浏览器集成测试按下文单独运行。

### 普通单元测试

在支持的 Linux 环境的模块目录执行：

```bash
make test
```

覆盖客户端配置、参数校验、错误分类、驱动状态、并发关闭和清理顺序，以及快照的格式、原始内容、权限、失败清理和源文件替换。使用 `core/testx` 和临时目录，不安装或启动驱动，不需要 Docker。命令开启竞态检测，运行环境需要 Go 及支持 CGO 的 C 编译器；首次整理或下载 Go 依赖可能联网。

### Compose 浏览器集成测试

先启动 Docker Desktop、OrbStack 或其他 Docker Engine，确认当前 Docker context 可用：

```bash
docker version
docker compose version
```

在模块目录执行：

```bash
make test-compose
```

宿主机只需要 Docker、Compose 和 make，无需本地 Go、Node.js 或浏览器驱动。该命令会：

1. 使用 `starudream/camoufox:1.0.1`，构建基于 `golang:1.27.2-bookworm` 的测试镜像。
2. 在测试镜像构建阶段，根据 `go.mod` 中的 Playwright 包准备配套驱动和 Node.js，将 Node 权限设为 `0755`，供运行用户 `1000:1000` 执行，再编译带竞态检测的测试执行器；此时不执行测试。
3. 启动 Camoufox，等待 `/json` 返回预期的 `/ws` 端点，再依次执行根包的普通单元测试和 `tests/` 的浏览器集成测试；任一阶段失败立即返回非零状态。
4. 输出 Go 测试结果；测试完成、失败或正常中断后，清理本次 Compose 项目的容器和网络。测试失败会使命令返回非零状态，已有失败状态不会被清理结果覆盖。

首次运行需要联网拉取镜像、Go 模块、驱动和 Node.js。测试运行阶段使用 Compose 内部网络，不发布宿主机端口、不挂载已有登录快照。测试网页通过 Playwright 路由拦截提供，不访问真实网站；KasmVNC 密码在浏览器容器内随机生成，不写入配置或输出。镜像构建上下文仅包含 Go 源码、模块文件和测试 Dockerfile，不包含 `.git` 或 `.state/`。

| 用例                                             | 验证内容                                                                             |
|--------------------------------------------------|--------------------------------------------------------------------------------------|
| `TestIntegrationConnectionAndIsolation`          | 连接、页面操作、同客户端会话隔离、多客户端隔离及重复关闭                             |
| `TestIntegrationPageFileTransfer`                | 通过测试页面上传本地文件，并将远程浏览器下载保存到客户端                             |
| `TestIntegrationStorageState`                    | Cookie、Local Storage、IndexedDB 保存恢复，默认不保存 IndexedDB，文件及新建目录权限  |
| `TestIntegrationStateFailures`                   | 缺失、损坏及无效快照拒绝恢复，恢复失败清理上下文，保存失败清理临时文件并保留已有数据 |
| `TestIntegrationReconnect`                       | 主动断开远程连接后旧对象失效，显式重连并从快照重建会话                               |
| `TestIntegrationDisconnectInterruptsPendingCall` | 本地驱动在线时主动断开，解除在途页面调用的等待并重连                                 |
| `TestIntegrationDriverExit`                      | Linux 下终止本次创建的本地驱动，检查 SDK 状态及显式重连后的快照恢复                  |
| `TestIntegrationRecoveryAfterDisconnectTimeout`  | 暂停本地驱动，验证断连超时后可关闭客户端或重连，旧驱动已退出                         |

浏览器测试位于 `tests/`，导入 SDK 并使用公开 API 验证完整流程；重连用例通过原生 `Session.Context().Browser()` 断开当前连接，不访问 SDK 私有字段。Linux 驱动退出用例通过 `/proc` 元数据定位本次连接新建的子进程并终止，不读取进程命令行或环境变量，不验证旧原生调用能否解除等待。断连超时恢复用例只暂停同样方式定位的本地驱动，在支持 pidfd 的 Linux 上验证强制回收。Compose 与 Dockerfile 放在同一目录，构建上下文仍为模块根目录。

以上用例测试受控页面上的存储行为，不证明真实网站登录、代理出口、KasmVNC 人工交互或容器崩溃恢复。浏览器测试源文件使用 `integration` 构建标签，并要求 `CAMOUFOX_INTEGRATION_TESTS=1`；普通 `make test` 不编译或运行浏览器用例。

### 保留现场排查

需要查看失败容器时，使用下面的分步命令；它们不会自动执行收尾清理：

```bash
docker compose -f tests/compose.yaml build tests
docker compose -f tests/compose.yaml up --abort-on-container-exit --exit-code-from tests
docker compose -f tests/compose.yaml ps -a
docker compose -f tests/compose.yaml logs --no-color camoufox tests
```

排查完成后清理：

```bash
docker compose -f tests/compose.yaml down --remove-orphans
```

默认项目名为 `camoufox-sdk-test`，清理仅作用于该项目。需要并行运行多组测试时，分别设置不同的 `COMPOSE_PROJECT_NAME`，同组命令使用同一项目名。清理保留镜像及构建缓存；快照只使用测试执行器内的临时目录。

在支持的 Linux 环境中仅做测试源码编译检查时，可以执行以下命令；它不会运行任何用例：

```bash
go test -c -race -o /tmp/camoufox-unit-tests .
go test -c -race -tags=integration -o /tmp/camoufox-integration-tests ./tests
```

静态检查、测试源码编译和 `docker compose config` 解析成功均不代表测试通过，实际结果以自行执行测试时的输出和退出状态为准。

[项目地图](PROJECT_MAP.md) · [协作规范](AGENTS.md) · [Apache-2.0](LICENSE)
