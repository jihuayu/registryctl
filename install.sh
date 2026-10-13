#!/bin/sh
set -eu

version='v0.1.0'
bin_dir="${HOME:?HOME 未设置}/.local/bin"
while [ "$#" -gt 0 ]; do
  case "$1" in
    --version) [ "$#" -ge 2 ] || { echo '--version 需要值' >&2; exit 1; }; version=$2; shift 2 ;;
    --bin-dir) [ "$#" -ge 2 ] || { echo '--bin-dir 需要值' >&2; exit 1; }; bin_dir=$2; shift 2 ;;
    --help|-h) echo '用法：sh install.sh [--version v0.1.0] [--bin-dir ~/.local/bin]'; exit 0 ;;
    *) echo "未知参数：$1" >&2; exit 1 ;;
  esac
done
printf '%s\n' "$version" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' || { echo 'version 必须是完整版本号 vMAJOR.MINOR.PATCH' >&2; exit 1; }
[ -n "$bin_dir" ] || { echo 'bin-dir 不能为空' >&2; exit 1; }
command -v curl >/dev/null || { echo '需要 curl' >&2; exit 1; }
case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  MINGW*|MSYS*|CYGWIN*) os=windows ;;
  *) echo '不支持此操作系统' >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo '仅支持 amd64 和 arm64' >&2; exit 1 ;;
esac
binary=registryctl
extension=tar.gz
if [ "$os" = windows ]; then binary=registryctl.exe; extension=zip; fi
archive="registryctl_${version#v}_${os}_${arch}.${extension}"
base="https://github.com/jihuayu/registryctl/releases/download/$version"
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM
curl --fail --silent --show-error --location --retry 3 --connect-timeout 15 --max-time 300 --proto '=https' --proto-redir '=https' --tlsv1.2 "$base/$archive" -o "$work_dir/$archive"
curl --fail --silent --show-error --location --retry 3 --connect-timeout 15 --max-time 60 --proto '=https' --proto-redir '=https' --tlsv1.2 "$base/SHA256SUMS" -o "$work_dir/SHA256SUMS"
expected=$(awk -v name="$archive" '$2 == name { print $1 }' "$work_dir/SHA256SUMS")
printf '%s\n' "$expected" | grep -Eq '^[a-f0-9]{64}$' || { echo '缺少唯一有效的下载校验值' >&2; exit 1; }
if command -v sha256sum >/dev/null; then
  actual=$(sha256sum "$work_dir/$archive" | awk '{print $1}')
elif command -v shasum >/dev/null; then
  actual=$(shasum -a 256 "$work_dir/$archive" | awk '{print $1}')
else
  echo '需要 sha256sum 或 shasum' >&2; exit 1
fi
[ "$actual" = "$expected" ] || { echo 'SHA-256 校验失败，拒绝安装' >&2; exit 1; }
if [ "$os" = windows ]; then
  unzip -p "$work_dir/$archive" "$binary" > "$work_dir/$binary"
else
  tar -xOzf "$work_dir/$archive" "$binary" > "$work_dir/$binary"
fi
[ -s "$work_dir/$binary" ] || { echo '发布包缺少二进制' >&2; exit 1; }
chmod 755 "$work_dir/$binary"
mkdir -p "$bin_dir"
cp "$work_dir/$binary" "$bin_dir/$binary"
"$bin_dir/$binary" version
printf '已安装到 %s\n' "$bin_dir/$binary"
