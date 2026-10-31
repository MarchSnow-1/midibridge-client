#!/usr/bin/env python3
"""生成发布清单 version.json，供外部程序判断是否安装某个版本

输入为已上传的产物目录，输出为单一 JSON 清单，其中 artifacts 数组按平台列出每个压缩包的下载地址、字节数与 SHA-256

Generate the release manifest version.json that lets external programs decide whether to install a build

The input is the directory holding the uploaded artifacts and the output is a single JSON manifest whose artifacts array lists the download URL, byte size and SHA-256 of every archive per platform
"""

import argparse
import hashlib
import json
import os
import subprocess
import sys
from datetime import datetime, timezone

# 协议契约的结构版本，用来让读取方先用一个整数判断能否直接比较协议版本
# ipcProtocol 是语义化字符串，比较它需要解析三段数字，而这个整数相等即表示协议字段的形态一致
# 只有在协议相关字段的形态变化时才递增，与 ipcProtocol 自身的语义化版本相互独立
#
# Structural version of the protocol contract, letting a reader judge comparability with a single integer before comparing versions
# ipcProtocol is a semantic string whose comparison needs three numeric parts, while equality on this integer already means the protocol fields have the same shape
# It is bumped only when the shape of the protocol-related fields changes, independent of the semantic version of ipcProtocol itself
IPC_PROTOCOL_SCHEMA = 1


def sha256_of(path):
    """计算文件的 SHA-256，返回小写十六进制字符串

    Compute the SHA-256 of a file and return lowercase hex
    """
    digest = hashlib.sha256()
    with open(path, "rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def git_value(args, default=""):
    """读取一个 git 值，失败时返回默认值而不是抛错

    Read a git value and fall back to the default instead of raising
    """
    try:
        out = subprocess.run(
            ["git"] + args,
            check=True,
            capture_output=True,
            text=True,
        )
        return out.stdout.strip()
    except Exception:
        return default


EXPECTED_PLATFORMS = [
    "windows-amd64",
    "linux-amd64",
    "linux-arm64",
    "linux-armv7",
    "darwin-amd64",
    "darwin-arm64",
]


def parse_platform(filename, version):
    """从产物文件名中解析平台名

    文件名形如 MIDIBridge-Client-<version>-<platform>，Windows 上带 .exe 后缀
    平台名按已知列表匹配而不是按点号切分，因为版本号本身可能含有点号
    解析失败时返回 None，由调用方记录为未知平台

    Parse the platform name out of an artifact filename

    The name looks like MIDIBridge-Client-<version>-<platform>, with an .exe suffix on Windows
    The platform is matched against the known list instead of splitting on dots, since a version string may contain dots
    A parse failure returns None and the caller records it as an unknown platform
    """
    prefix = "MIDIBridge-Client-%s-" % version
    if not filename.startswith(prefix):
        return None
    rest = filename[len(prefix):]
    if rest.endswith(".exe"):
        rest = rest[:-len(".exe")]
    return rest or None


def build_artifact(path, version, download_base):
    """构造单个平台条目

    Build one platform entry
    """
    filename = os.path.basename(path)
    platform = parse_platform(filename, version)

    # arch 直接承载平台标识中 os 之后的部分，因此 linux-armv7 的 arch 就是 armv7
    # 这样读取方用 GOOS 加 GOARCH 即可直接比对，armv7 之类的细分也保留在同一个字段里
    # 无法解析时留空 os 与 arch，但仍保留条目，方便外部发现异常产物
    #
    # arch carries everything after the os segment, so the arch of linux-armv7 is armv7
    # A reader can therefore compare GOOS and GOARCH directly, and a finer value such as armv7 stays in the same field
    # An unparsable name keeps empty os and arch while the entry stays, so external programs can spot an unexpected artifact
    os_name = ""
    arch = ""
    if platform:
        parts = platform.split("-", 1)
        if len(parts) == 2 and parts[0] and parts[1]:
            os_name, arch = parts

    # dev 产物没有稳定的公开下载地址，url 置为 null 而不是拼出一个会失效的链接
    # 外部据此改用 run 页面的 artifact 下载，判断逻辑与正式发布区分开
    #
    # Dev builds have no stable public URL, so url is null rather than a link that would expire
    # External programs then fall back to the run page artifact, which keeps the decision logic separate from a release
    url = "%s/%s" % (download_base.rstrip("/"), filename) if download_base else None

    return {
        "platform": platform or "",
        "os": os_name,
        "arch": arch,
        "filename": filename,
        "size": os.path.getsize(path),
        "sha256": sha256_of(path),
        "url": url,
    }


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--channel", required=True, choices=["release", "dev"])
    parser.add_argument("--version", required=True)
    parser.add_argument("--ipc-protocol", required=True)
    parser.add_argument("--assets-dir", required=True)
    parser.add_argument("--out", required=True)
    parser.add_argument("--repo", default="", help="owner/repository, defaults to GITHUB_REPOSITORY")
    args = parser.parse_args()

    repo = args.repo or os.environ.get("GITHUB_REPOSITORY", "")
    if not repo:
        print("missing --repo or GITHUB_REPOSITORY", file=sys.stderr)
        return 1

    server = os.environ.get("GITHUB_SERVER_URL", "https://github.com")

    # 去掉 tag 的 v 前缀，便于外部做语义化比较，原样值保留在 version 字段
    #
    # Strip the leading v so external programs can compare semantically, while the original text stays in version
    version_semver = args.version[1:] if args.version.startswith("v") else args.version

    artifacts = []
    warnings = []
    prefix = "MIDIBridge-Client-%s-" % args.version
    for name in sorted(os.listdir(args.assets_dir)):
        # 产物是裸二进制，没有统一后缀，因此按文件名前缀识别，Windows 上带 .exe
        #
        # Artifacts are bare binaries without a shared suffix, so they are recognised by filename prefix, with .exe on Windows
        if not name.startswith(prefix):
            continue
        path = os.path.join(args.assets_dir, name)
        if not os.path.isfile(path):
            continue
        if args.channel == "release":
            download_base = "%s/%s/releases/download/%s" % (server, repo, args.version)
        else:
            # dev 产物只存在于 Actions run 的 artifact 中，没有稳定的公开下载地址
            # 因此 url 留空并由外部改用 run 页面获取，避免给出会失效的链接
            #
            # Dev builds exist only as Actions run artifacts and have no stable public URL
            # The url is therefore left empty so external programs use the run page, avoiding a link that expires
            download_base = ""
        artifact = build_artifact(path, args.version, download_base)
        if not artifact["platform"]:
            warnings.append("unrecognised artifact name: %s" % name)
        artifacts.append(artifact)

    if not artifacts:
        print("no artifacts found in %s" % args.assets_dir, file=sys.stderr)
        return 1

    found = {item["platform"] for item in artifacts}
    for platform in EXPECTED_PLATFORMS:
        if platform not in found:
            warnings.append("missing expected platform: %s" % platform)

    commit = git_value(["rev-parse", "HEAD"])
    manifest = {
        "client": "midibridge-client",
        "channel": args.channel,
        "version": args.version,
        "versionSemver": version_semver,
        "ipcProtocol": args.ipc_protocol,
        "ipcProtocolSchema": IPC_PROTOCOL_SCHEMA,
        "commit": commit,
        "commitShort": commit[:7] if commit else "",
        "builtAt": int(datetime.now(timezone.utc).timestamp()),
        "source": "%s/%s" % (server, repo),
        "releaseUrl": (
            "%s/%s/releases/tag/%s" % (server, repo, args.version)
            if args.channel == "release"
            else None
        ),
        "changelogUrl": (
            "%s/%s/blob/%s/CHANGELOG.md" % (server, repo, args.version)
            if args.channel == "release"
            else None
        ),
        "artifacts": artifacts,
        "warnings": warnings,
    }

    with open(args.out, "w", encoding="utf-8", newline="\n") as handle:
        json.dump(manifest, handle, indent=2, ensure_ascii=False)
        handle.write("\n")

    print("wrote %s with %d artifact(s)" % (args.out, len(artifacts)))
    if warnings:
        for warning in warnings:
            print("warning: %s" % warning, file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
