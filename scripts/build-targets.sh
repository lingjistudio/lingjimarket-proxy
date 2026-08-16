#!/usr/bin/env bash
# 构建 sing-box 多平台二进制
# 用法: ./scripts/build-targets.sh [darwin/arm64] [windows/amd64] ...
# 无参数时默认构建: darwin/arm64 windows/amd64

set -euo pipefail

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$PROJECT_ROOT"

OUT_DIR="${OUT_DIR:-$PROJECT_ROOT/dist}"
VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
LDFLAGS_SHARED="$(cat release/LDFLAGS 2>/dev/null || echo '')"

# 全协议支持构建标签：
#   with_clash_api        HTTP API（POST /outbounds、PUT /proxies 等，见 docs/api-outbound.md）
#   with_quic             hysteria / hysteria2 / tuic 出站
#   with_naive_outbound   naive 出站（CGO_ENABLED=0 时依赖 cronet 纯 Go 绑定，需 with_purego）
#   with_utls             TLS 指纹 / REALITY
#   with_gvisor/with_dhcp/with_wireguard/with_acme/with_tailscale  其他能力
TAGS="with_clash_api,with_quic,with_naive_outbound,with_purego,with_utls,with_gvisor,with_dhcp,with_wireguard,with_acme,with_tailscale"

LDFLAGS="-X 'github.com/sagernet/sing-box/constant.Version=${VERSION}' ${LDFLAGS_SHARED} -s -w -buildid="

mkdir -p "$OUT_DIR"

TARGETS=("$@")
if [ ${#TARGETS[@]} -eq 0 ]; then
  TARGETS=("darwin/arm64" "windows/amd64")
fi

echo "==> version : $VERSION ($COMMIT)"
echo "==> tags    : $TAGS"
echo "==> output  : $OUT_DIR"

for target in "${TARGETS[@]}"; do
  GOOS="${target%%/*}"
  GOARCH="${target##*/}"
  case "$GOOS" in
    windows) EXT=".exe" ;;
    *)       EXT="" ;;
  esac
  echo "==> building ${GOOS}/${GOARCH} ..."
  CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" \
    go build -v -trimpath \
    -tags "$TAGS" \
    -ldflags "$LDFLAGS" \
    -o "${OUT_DIR}/sing-box-${GOOS}-${GOARCH}${EXT}" \
    ./cmd/sing-box
done

echo "==> done. artifacts:"
ls -lh "$OUT_DIR"
