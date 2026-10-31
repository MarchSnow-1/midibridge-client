# MIDIBridge Client — 发布清单

[English](version-manifest.md) | [简体中文](version-manifest_zh-CN.md)

本文档说明发布产物附带的 `version.json`，它让外部程序在下载之前就能判断某个版本是否适合自己

---

## 1. 用途

每次发布与每次 dev 构建都会生成一份 `version.json`，内容为当前构建的全部平台产物及其校验信息

外部程序读取这份清单即可回答三个问题，无需下载产物本体

- 这个版本的程序版本号与 ipc 协议版本是否兼容
- 有没有适合我当前平台与架构的产物
- 下载下来的文件是否与发布时一致

## 2. 获取方式

正式发布时清单位于 Release 页面的资产列表中，文件名固定为 `version.json`

```
https://github.com/<owner>/<repo>/releases/download/<version>/version.json
```

dev 构建不发布 Release，清单作为 Actions run 的 artifact 上传，artifact 名固定为 `version.json`，不随版本号变化，用 `gh run download <run-id> -n version.json` 或从该次运行页面取得

各平台产物同样是裸二进制的 artifact，名为 `MIDIBridge-Client-<version>-<platform>`，Windows 上带 `.exe` 后缀

`channel` 字段区分两种来源，正式发布为 `release`，dev 构建为 `dev`

## 3. 顶层字段

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `client` | 字符串 | 固定为 `midibridge-client`，用于确认清单属于哪个程序 |
| `channel` | 字符串 | `release` 或 `dev` |
| `version` | 字符串 | 版本标识原样值，正式发布为 tag 形式如 `v1.3.0`，dev 为 `git describe` 推导值 |
| `versionSemver` | 字符串 | 去掉 `v` 前缀的版本号，便于做语义化比较 |
| `ipcProtocol` | 字符串 | ipc 协议版本，与握手帧 `proto` 字段一致 |
| `ipcProtocolSchema` | 整数 | 描述对应版本的程序所使用的协议的主要版本，跨主要版本代表不向下兼容 |
| `commit` | 字符串 | 完整提交哈希，用于追溯来源 |
| `commitShort` | 字符串 | 短提交哈希，便于人读 |
| `builtAt` | 整数 | 构建时间的 Unix 时间戳，单位为秒 |
| `source` | 字符串 | 仓库地址 |
| `releaseUrl` | 字符串或 null | 正式发布的 Release 页面，dev 构建为 `null` |
| `changelogUrl` | 字符串或 null | 该版本的 CHANGELOG 地址，dev 构建为 `null` |
| `artifacts` | 数组 | 各平台产物的条目，见下一节 |
| `warnings` | 数组 | 生成时的告警，例如缺少某个预期平台 |

## 4. artifacts 条目字段

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `platform` | 字符串 | 平台标识，与产物文件名中的平台段一致 |
| `os` | 字符串 | 平台标识中横线之前的部分，对应 Go 的 `GOOS`，无法解析时为空串 |
| `arch` | 字符串 | 平台标识中横线之后的部分，对应 Go 的 `GOARCH`，armv7 一类细分也完整保留在此字段，无法解析时为空串 |
| `filename` | 字符串 | 资产文件名 |
| `size` | 整数 | 文件字节数，可用于下载前预估空间 |
| `sha256` | 字符串 | 文件的小写十六进制 SHA-256，用于下载后校验 |
| `url` | 字符串或 null | 直接下载地址，dev 构建为 `null` |

`os` 与 `arch` 拼起来即为 `platform`，因此外部程序可以直接用 `runtime.GOOS` 与 `runtime.GOARCH` 比对

例如 Go 程序可以这样挑选产物

```go
want := runtime.GOOS + "-" + runtime.GOARCH
```

arm 平台的细分版本写在 `arch` 里，例如 `linux-armv7` 的 `arch` 为 `armv7`，因此按 `GOARCH` 比对时需要单独处理这类平台

## 5. 判断是否安装

以下条件按顺序判断，任一不满足即表示该版本不适合当前使用方

1. **协议兼容** `ipcProtocol` 的主版本号与使用方期望的一致，主版本不同表示握手帧结构可能已变化
2. **平台匹配** `artifacts` 中存在 `os` 与 `arch` 与当前运行环境一致的条目
3. **版本取舍** 用 `versionSemver` 与本地已装版本做语义化比较，`channel` 为 `dev` 的构建通常只用于测试
4. **完整性校验** 下载完成后比对 `size` 与 `sha256`，不一致说明传输过程出错或资产被替换

## 6. 与握手帧的关系

清单中的 `ipcProtocol` 与运行期握手帧的 `proto` 字段取自同一常量，因此两者不会出现不一致

若两者不同，说明资产与清单不匹配，应当重新下载
