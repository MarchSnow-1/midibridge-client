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

需要跟随最新正式版时，用 `releases/latest/download` 这个路径，`latest` 是 release 段的别名而不是 tag 段的别名

```
https://github.com/<owner>/<repo>/releases/latest/download/version.json
```

`latest` 指向最新的正式 Release，标记为 prerelease 或 draft 的发布不会成为 `latest`

dev 构建不发布 Release，清单作为 Actions run 的 artifact 上传，artifact 名固定为 `version.json`，不随版本号变化，用 `gh run download <run-id> -n version.json` 或从该次运行页面取得

各平台产物同样是裸二进制的 artifact，名为 `MIDIBridge-Client-<version>-<platform>`，Windows 上带 `.exe` 后缀

`channel` 字段区分两种来源，正式发布为 `release`，dev 构建为 `dev`

## 3. 使用 latest 时的约束

用 `latest` 检测更新时，下载必须走清单里该平台条目的 `url`，不要自己在同一个 `latest` 地址上拼平台文件名

原因有两点，都在两次请求之间发生新发布时暴露

- 清单来自旧版本而产物来自新版本，`sha256` 与实际文件对不上，完整性校验失败
- 新版本可能改变了清单格式或字段含义，按旧逻辑解析新清单会得到错误结果

清单里的 `url` 是含具体 tag 的绝对地址，用它可以锁定与清单同一版本的产物，因此只要顺序正确就不会出现上面两种情况

```
1  GET .../releases/latest/download/version.json
2  读取 artifacts 数组，按 os 与 arch 选出本平台条目
3  GET 该条目的 url          ← 地址里已带具体 tag，与第 1 步的清单必然同版本
4  比对 size 与 sha256
```

需要固定版本时直接使用具体 tag 而不是 `latest`，两次请求就都锁定在同一个版本上

dev 构建的 `url` 为 `null`，因为 Actions run 的 artifact 没有稳定的公开地址，这时按 `filename` 从该次运行的 artifact 中取

## 4. 顶层字段

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `client` | 字符串 | 固定为 `midibridge-client`，用于确认清单属于哪个程序 |
| `channel` | 字符串 | `release` 或 `dev` |
| `version` | 字符串 | 版本标识原样值，正式发布为 tag 形式如 `v1.3.0`，dev 为 `git describe` 推导值 |
| `versionSemver` | 字符串 | 去掉 `v` 前缀的版本号，便于做语义化比较 |
| `ipcProtocol` | 字符串 | ipc 协议版本，与握手帧 `proto` 字段一致 |
| `ipcProtocolSchema` | 整数 | 描述对应版本的程序所使用的协议的主要版本，跨主要版本代表不向下兼容，它就是 `ipcProtocol` 的第一段，例如 `ipcProtocol` 为 `1.0.0` 时本字段为 `1`，为 `2.30.77` 时为 `2`，供读取方先用一个整数判断而无需每次解析字符串 |
| `commit` | 字符串 | 完整提交哈希，用于追溯来源 |
| `commitShort` | 字符串 | 短提交哈希，便于人读 |
| `builtAt` | 整数 | 构建时间的 Unix 时间戳，单位为秒 |
| `source` | 字符串 | 仓库地址 |
| `releaseUrl` | 字符串或 null | 正式发布的 Release 页面，dev 构建为 `null` |
| `changelogUrl` | 字符串或 null | 该版本的 CHANGELOG 地址，dev 构建为 `null` |
| `artifacts` | 数组 | 各平台产物的条目，见下一节 |
| `warnings` | 数组 | 生成时的告警，例如缺少某个预期平台 |

## 5. artifacts 条目字段

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

## 6. 判断是否安装

以下条件按顺序判断，任一不满足即表示该版本不适合当前使用方

1. **协议兼容** 先比 `ipcProtocolSchema` 是否与使用方期望的整数相等，相等即表示主版本一致，不相等代表跨主版本因而不向下兼容，两者相等时再按需比对 `ipcProtocol` 的次版本
2. **平台匹配** `artifacts` 中存在 `os` 与 `arch` 与当前运行环境一致的条目
3. **版本取舍** 用 `versionSemver` 与本地已装版本做语义化比较，`channel` 为 `dev` 的构建通常只用于测试
4. **完整性校验** 下载完成后比对 `size` 与 `sha256`，不一致说明传输过程出错、资产被替换，或者两次请求之间发生了新发布，第 3 节说明了如何避免最后一种

## 7. 与握手帧的关系

清单中的 `ipcProtocol` 与运行期握手帧的 `proto` 字段取自同一常量，因此两者不会出现不一致

若两者不同，说明资产与清单不匹配，应当重新下载
