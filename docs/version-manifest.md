# MIDIBridge Client — Release manifest

[English](version-manifest.md) | [简体中文](version-manifest_zh-CN.md)

This document describes the `version.json` shipped with every build, which lets an external program decide whether a given version suits it before downloading anything

---

## 1. Purpose

Each release and each dev build produces a `version.json` listing every platform artifact of that build together with its integrity data

An external program that reads this manifest can answer three questions without downloading the artifacts themselves

- whether the program version and the ipc protocol version are compatible
- whether an artifact exists for the platform and architecture it runs on
- whether a downloaded file matches what was published

## 2. How to obtain it

For a release the manifest sits in the asset list of the Release page under the fixed name `version.json`

```
https://github.com/<owner>/<repo>/releases/download/<version>/version.json
```

A dev build publishes no release, so the manifest is uploaded as an Actions run artifact under the fixed name `version.json`, which does not change with the build, and is fetched with `gh run download <run-id> -n version.json` or from that run page

The platform artifacts are bare binary artifacts too, named `MIDIBridge-Client-<version>-<platform>`, with an `.exe` suffix on Windows

The `channel` field distinguishes the two sources, `release` for a published version and `dev` for a development build

## 3. Top-level fields

| Field | Type | Meaning |
| --- | --- | --- |
| `client` | string | Fixed to `midibridge-client`, identifying which program the manifest belongs to |
| `channel` | string | `release` or `dev` |
| `version` | string | Version identifier as-is, a tag such as `v1.3.0` for a release and a `git describe` derived value for a dev build |
| `versionSemver` | string | Version with the `v` prefix removed, for semantic comparison |
| `ipcProtocol` | string | ipc protocol version, identical to the `proto` field of the handshake frame |
| `ipcProtocolSchema` | integer | Major version of the protocol used by the program in that release, where crossing a major version means backward compatibility is not kept |
| `commit` | string | Full commit hash, for tracing the source |
| `commitShort` | string | Short commit hash, for readability |
| `builtAt` | integer | Build time as a Unix timestamp in seconds |
| `source` | string | Repository URL |
| `releaseUrl` | string or null | Release page of a published version, `null` for a dev build |
| `changelogUrl` | string or null | CHANGELOG URL for that version, `null` for a dev build |
| `artifacts` | array | One entry per platform artifact, see the next section |
| `warnings` | array | Warnings from generation, such as a missing expected platform |

## 4. Artifact entry fields

| Field | Type | Meaning |
| --- | --- | --- |
| `platform` | string | Platform identifier, matching the platform segment of the artifact filename |
| `os` | string | The part before the hyphen in the platform identifier, matching the Go `GOOS`, empty when unparsable |
| `arch` | string | The part after the hyphen, matching the Go `GOARCH`, keeping a finer value such as armv7 as-is, empty when unparsable |
| `filename` | string | Asset filename |
| `size` | integer | File size in bytes, useful for estimating space before downloading |
| `sha256` | string | Lowercase hex SHA-256 of the file, for verifying a download |
| `url` | string or null | Direct download URL, `null` for a dev build |

`os` and `arch` join back into `platform`, so an external program can compare them against `runtime.GOOS` and `runtime.GOARCH`

A Go program can pick its artifact like this

```go
want := runtime.GOOS + "-" + runtime.GOARCH
```

A finer ARM version lives in `arch`, so the arch of `linux-armv7` is `armv7`, and such a platform needs its own handling when comparing against `GOARCH`

## 5. Deciding whether to install

Evaluate these conditions in order, and treat the version as unsuitable when any of them fails

1. **Protocol is compatible** the major version of `ipcProtocol` matches what the consumer expects, since a different major version means the handshake frame may have changed shape
2. **Platform matches** `artifacts` holds an entry whose `os` and `arch` match the current runtime
3. **Version is acceptable** compare `versionSemver` against the installed version semantically, and note that a build with `channel` set to `dev` is normally meant for testing only
4. **Integrity holds** after downloading, compare `size` and `sha256`, where a mismatch means a broken transfer or a replaced asset

## 6. Relationship to the handshake frame

The `ipcProtocol` field and the runtime `proto` field read the same constant, so they cannot disagree with each other

A difference between them means the artifact and the manifest do not belong together and the download should be repeated
