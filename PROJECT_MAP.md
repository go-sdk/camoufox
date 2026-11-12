# 项目地图

## 项目定位

`github.com/go-sdk/camoufox` 提供 Camoufox 远程 Firefox 连接、独立会话和登录快照管理，页面操作使用原生 Playwright API。使用流程、配置、安全和支持边界以 [README](README.md) 为准。

仅支持 `linux/amd64` 和 `linux/arm64`，不维护其他操作系统或架构的兼容实现。

## 目录结构

```text
camoufox/
├── .editorconfig                   编辑器格式规范
├── .dockerignore                   测试镜像构建上下文白名单
├── .gitignore                      本地登录快照目录忽略规则
├── AGENTS.md                       仓库协作规范
├── client.go                       Client、默认值、Options 和连接生命周期
├── driver_linux.go                 Linux 驱动进程查询、终止及查询句柄释放
├── session.go                      独立上下文创建、注册、恢复和关闭
├── state.go                        原始快照校验、受限临时文件及保存替换
├── client_test.go                  无浏览器的客户端配置、校验和错误分类测试
├── session_test.go                 无浏览器的会话关闭与快照恢复测试
├── state_test.go                   无浏览器的快照校验、权限及保存替换测试
├── tests/                          仅使用公开 API 的浏览器集成测试
│   ├── integration_test.go         连接、隔离、快照和显式重连测试
│   ├── driver_linux_test.go        本地驱动退出、暂停及断连超时恢复测试
│   ├── compose.yaml                浏览器和测试容器的独立编排
│   └── Dockerfile                  预装驱动并编译带竞态检测的测试执行器
├── error.go                        哨兵错误及安全的上游错误分类
├── doc.go                          根包说明
├── README.md                       使用说明及契约统一入口
├── LICENSE                         Apache-2.0 许可证
├── Makefile                        依赖整理、格式化、静态检查、编译和测试入口
├── go.mod                          模块及依赖
└── go.sum                          依赖校验
```

## 调用链

```text
New
    -> 保存配置，不执行外部操作

Client.Connect
    -> 检查本地驱动及连接状态，校验端点和超时，清理失效连接与旧会话
    -> playwright.Run 启动预先安装的驱动
    -> 持有独立进程查询句柄，子进程仍由上游回收
    -> Firefox.Connect 连接完整端点，传递握手 Header 和超时

Client.NewSession
    -> 读取并校验可选快照，移除传给上游的 StorageStatePath
    -> Browser.NewContext 创建独立上下文
    -> 通过受限临时文件将已校验的 JSON 交给 Context.SetStorageState
    -> 注册会话，返回 Session

Session.Context
    -> 原生 BrowserContext -> NewPage -> 业务页面操作

Session.SaveState
    -> 创建同目录 0600 临时文件
    -> Context.StorageState 写入原始 JSON
    -> 校验快照、Sync、Rename 替换目标

Session.Close
    -> Context.Close -> 解除注册

Client.Close
    -> 未发起断连：清理会话 -> Browser.Close -> Playwright.Stop
    -> 已发起断连：接管旧驱动清理，超时则尝试通过 pidfd 终止
    -> 释放进程查询句柄，等待并发 Connect 并合并其清理错误
```

`Client.Disconnect(ctx)` 可在本地驱动仍能处理请求时先断开远程连接，并发请求共享一次断连，失败可重试；关闭或重连会接管已发起的断连。客户端和会话的状态由互斥锁保护，远程调用和等待在状态锁外执行；`saveMu` 串行化同一会话的快照保存。创建的上下文在返回前检查所属连接，无法发布时清理；原生关闭事件更新会话状态，若 SDK 已开始关闭则由该调用解除注册。并发关闭及断线后的处理方式见 [README](README.md#生命周期)。

`clientDriver` 的进程锁串行化本地存活查询与查询句柄释放，兼容 Linux 未提供 pidfd 时的 PID 查询；强制终止要求 pidfd；协议关闭和等待子进程退出在进程锁外执行，子进程由上游回收。

SDK 检测到本地驱动退出后拒绝新会话及旧快照保存，跳过失效驱动上的远程关闭请求，允许显式重连。当前上游无法通过公开 API 解除驱动退出前已经挂起的原生调用；SDK 状态检查与新连接恢复不代表该上游问题已解决，详见 README 的生命周期限制。

## 依赖与验证

直接依赖 `core` 和 `github.com/mxschmitt/playwright-go`，错误处理使用 `core/errx`，快照外层校验使用 `core/codec/json`。保存和恢复使用原始快照文件，不经过上游 Go 状态模型，避免 IndexedDB 字段丢失。SDK 错误只保留安全类别，不透出上游原始文本。

`golang-set/v2` 当前使用 `v2.8.0`，排除包含 MongoDB 驱动的 `v2.9.0`；不跨主版本替换。排除规则的下游适用范围见 [README](README.md#环境要求与安装)。

本目录是独立 Go 模块与 Git 仓库，无需修改相邻 SDK 或浏览器镜像。版本对应关系和部署准备见 [README](README.md#环境要求与安装)。

`make fmt` 格式化代码，`make lint` 整理依赖并检查包括 integration 标签在内的源码，`make build` 仅编译生产包。普通单元测试使用 `core/testx`，不启动驱动或浏览器。浏览器测试由 `integration` 构建标签和环境变量共同启用，流程见 [README](README.md#测试流程)。

`tests/compose.yaml` 编排 Camoufox 与测试执行器，后者等待浏览器健康检查成功，再依次运行根包单元测试和独立集成测试包。测试镜像使用模块锁定的 Playwright CLI 准备驱动及 Node.js，显式将 Node 权限设为 `0755`，供运行用户 `1000:1000` 执行；构建期间仅编译两份带竞态检测的测试执行器，不执行用例。运行阶段使用内部网络和路由拦截页面，不依赖外站、宿主机端口或已有快照。

未经明确授权不运行测试、示例、驱动、浏览器或外部接口；静态检查、Compose 配置解析和编译不能证明容器或浏览器运行成功。
