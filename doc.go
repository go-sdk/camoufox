// Package camoufox 封装 Camoufox 的远程 Firefox Playwright 连接、会话和登录快照。
//
// 页面操作使用原生 Playwright API；本地驱动需提前准备，SDK 不自动安装依赖。
// 仅支持 linux/amd64 和 linux/arm64。
// 断线后由调用方显式重连并重新创建会话，不自动重放页面操作。
package camoufox
