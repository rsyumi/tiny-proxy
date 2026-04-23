#!/usr/bin/env bash
set -e

# ── 경로 설정 ────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

if [ ! -f "$PROJECT_DIR/main.go" ]; then
    echo "ERROR: tiny-proxy 프로젝트의 scripts/ 폴더 안에서 실행해야 합니다."
    exit 1
fi

OWNER="rsyumi"
REPO="tiny-proxy"
EXE_PATH="$PROJECT_DIR/tiny-proxy"
ENV_FILE="$PROJECT_DIR/.env"
ENV_EXAMPLE="$PROJECT_DIR/.env.example"

# ── 색상 ─────────────────────────────────────────────
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
GRAY='\033[0;90m'
NC='\033[0m'

# ── 의존성 설치 헬퍼 ────────────────────────────────
pkg_install() {
    if command -v pkg &>/dev/null; then
        pkg install -y "$1"
    elif command -v apt-get &>/dev/null; then
        apt-get install -y "$1"
    else
        echo -e "${RED}  ERROR: $1을 설치할 수 없습니다. 직접 설치해주세요.${NC}"
        exit 1
    fi
}

ensure_cmd() {
    local cmd="$1" pkg="${2:-$1}"
    if command -v "$cmd" &>/dev/null; then return; fi
    echo -e "${YELLOW}  $pkg 설치 중...${NC}"
    pkg_install "$pkg"
}

ensure_cmd curl
ensure_cmd ssh openssh

# ── 배너 ─────────────────────────────────────────────
clear
echo ""
echo -e "${CYAN}  ==========================================${NC}"
echo -e "${CYAN}    tiny-proxy  Setup (Linux / Termux)${NC}"
echo -e "${CYAN}  ==========================================${NC}"
echo ""

# ── 아키텍처 감지 ────────────────────────────────────
ARCH="$(uname -m)"
case "$ARCH" in
    aarch64|arm64) GOARCH="arm64" ;;
    x86_64)        GOARCH="amd64" ;;
    *)
        echo -e "${RED}  ERROR: 지원하지 않는 아키텍처입니다: $ARCH${NC}"
        exit 1
        ;;
esac
echo -e "${GRAY}  아키텍처: $ARCH ($GOARCH)${NC}"

# ── 1. 최신 바이너리 다운로드 ────────────────────────
echo -e "${YELLOW}  [1/2] GitHub Releases에서 최신 바이너리 다운로드 중...${NC}"

RELEASE_URL="https://api.github.com/repos/$OWNER/$REPO/releases/latest"
RELEASE_JSON="$(curl -sL -H 'User-Agent: tiny-proxy-setup' "$RELEASE_URL")"

DOWNLOAD_URL="$(echo "$RELEASE_JSON" | grep -o "\"browser_download_url\": *\"[^\"]*linux-${GOARCH}\"" | head -1 | cut -d'"' -f4)"

if [ -z "$DOWNLOAD_URL" ]; then
    echo -e "${RED}    WARNING: linux-${GOARCH} 바이너리를 찾을 수 없습니다.${NC}"
    if [ ! -f "$EXE_PATH" ]; then
        echo -e "${RED}    go build -o tiny-proxy . 로 직접 빌드하세요.${NC}"
        exit 1
    fi
    echo -e "${YELLOW}    기존 바이너리를 사용합니다.${NC}"
else
    ASSET_NAME="$(basename "$DOWNLOAD_URL")"
    echo -e "${GREEN}    $ASSET_NAME${NC}"
    curl -sL -o "$EXE_PATH" "$DOWNLOAD_URL"
    chmod +x "$EXE_PATH"
    echo -e "${GREEN}    다운로드 완료${NC}"
fi

# ── 2. .env 파일 설정 ────────────────────────────────
echo ""
echo -e "${YELLOW}  [2/2] .env 파일 설정 중...${NC}"

if [ ! -f "$ENV_FILE" ]; then
    if [ -f "$ENV_EXAMPLE" ]; then
        cp "$ENV_EXAMPLE" "$ENV_FILE"
    else
        cat > "$ENV_FILE" <<'EOF'
PORT=8080
TIMEOUT=30m
AUTH_MODE=none
HEADER_URL=Yumi-Url
HEADER_PREFIX=Yumi-H-
HEADER_BULK=Yumi-Headers
HEADER_AUTH=Yumi-Auth
EOF
    fi
    echo -e "${GREEN}    .env 생성 완료${NC}"
else
    echo -e "${GRAY}    .env가 이미 존재합니다. 유지합니다.${NC}"
fi

# ── 포트 읽기 ────────────────────────────────────────
PORT="$(grep -E '^\s*PORT\s*=' "$ENV_FILE" | head -1 | cut -d= -f2 | tr -d '[:space:]')"
PORT="${PORT:-8080}"

# ── 모드 선택 ────────────────────────────────────────
echo ""
echo -e "${CYAN}  사용 방식을 선택하세요:${NC}"
echo "    1) 로컬 실행만          (http://localhost:$PORT)"
echo "    2) 로컬 HTTPS           (인증서 자동 생성)"
echo "    3) 외부 터널 - Pinggy        (설치 불필요, 60분 제한)"
echo "    4) 외부 터널 - localhost.run  (설치 불필요, 제한 없음)"
echo "    5) 외부 터널 - Cloudflare    (자동 설치, 제한 없음)"
echo ""
read -rp "  선택 [1-5] (기본: 1): " MODE_CHOICE
MODE_CHOICE="${MODE_CHOICE:-1}"

# ── SSH 키 준비 (터널 모드 3, 4) ─────────────────────
SSH_KEY="$SCRIPT_DIR/.ssh_key"
ensure_ssh_key() {
    if [ -f "$SSH_KEY" ]; then return; fi
    echo -e "${YELLOW}  터널용 SSH 키 생성 중...${NC}"
    ssh-keygen -t ed25519 -f "$SSH_KEY" -N "" -q
    echo -e "${GREEN}    SSH 키 생성 완료${NC}"
}

# ── cloudflared 설치 헬퍼 ────────────────────────────
CLOUDFLARED_PATH="$SCRIPT_DIR/cloudflared"
ensure_cloudflared() {
    if [ -f "$CLOUDFLARED_PATH" ]; then return; fi
    if command -v cloudflared &>/dev/null; then
        CLOUDFLARED_PATH="$(command -v cloudflared)"
        return
    fi
    echo -e "${YELLOW}  cloudflared 다운로드 중...${NC}"
    local cf_url="https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-${GOARCH}"
    curl -sL -o "$CLOUDFLARED_PATH" "$cf_url"
    chmod +x "$CLOUDFLARED_PATH"
    echo -e "${GREEN}    cloudflared 설치 완료${NC}"
}

# ── .env에 TLS 설정 반영 ────────────────────────────
set_env_value() {
    local key="$1" value="$2"
    if grep -qE "^\s*#?\s*${key}\s*=" "$ENV_FILE"; then
        sed -i "s|^\s*#\?\s*${key}\s*=.*|${key}=${value}|" "$ENV_FILE"
    else
        echo "${key}=${value}" >> "$ENV_FILE"
    fi
}

# ── TLS 설정 반영 ────────────────────────────────────
if [ "$MODE_CHOICE" = "2" ]; then
    CERT_FILE="$SCRIPT_DIR/cert.pem"
    KEY_FILE="$SCRIPT_DIR/key.pem"

    if [ ! -f "$CERT_FILE" ] || [ ! -f "$KEY_FILE" ]; then
        echo ""
        echo -e "${YELLOW}  TLS 인증서 생성 중...${NC}"
        bash "$SCRIPT_DIR/gen-cert.sh"
    else
        echo ""
        echo -e "${GRAY}  TLS 인증서가 이미 존재합니다.${NC}"
    fi

    set_env_value TLS_CERT "scripts/cert.pem"
    set_env_value TLS_KEY  "scripts/key.pem"
else
    set_env_value TLS_CERT ""
    set_env_value TLS_KEY  ""
fi

# ── 터널 모드 무인증 경고 ────────────────────────────
AUTH_MODE="$(grep -E '^\s*AUTH_MODE\s*=' "$ENV_FILE" | head -1 | cut -d= -f2 | tr -d '[:space:]')"
if [[ "$MODE_CHOICE" =~ ^[345]$ ]] && [ "$AUTH_MODE" != "key" ]; then
    echo ""
    echo -e "${RED}  ※ 주의: 인증 없이 터널을 열면 누구나 이 프록시를 사용할 수 있습니다.${NC}"
    echo -e "${RED}    .env에서 AUTH_MODE=key / AUTH_KEY=<비밀번호>를 설정하는 것을 권장합니다.${NC}"
fi

# ── 실행 ─────────────────────────────────────────────
echo ""
echo -e "${GREEN}  ==========================================${NC}"
if [ "$MODE_CHOICE" = "2" ]; then
    echo -e "${GREEN}    tiny-proxy 실행 중${NC}"
    echo -e "${GREEN}    https://localhost:$PORT${NC}"
else
    echo -e "${GREEN}    tiny-proxy 실행 중${NC}"
    echo -e "${GREEN}    http://localhost:$PORT${NC}"
fi
echo -e "${GREEN}  ==========================================${NC}"
echo ""

case "$MODE_CHOICE" in
    3|4|5)
        # ── 터널 모드: 프록시 + 터널 동시 실행 ──────
        PROXY_PID=""
        TUNNEL_PID=""

        cleanup() {
            [ -n "$PROXY_PID"  ] && kill "$PROXY_PID"  2>/dev/null || true
            [ -n "$TUNNEL_PID" ] && kill "$TUNNEL_PID" 2>/dev/null || true
            echo ""
            echo -e "${YELLOW}  프록시와 터널이 종료되었습니다.${NC}"
        }
        trap cleanup EXIT

        cd "$PROJECT_DIR"
        "$EXE_PATH" &
        PROXY_PID=$!
        sleep 2

        case "$MODE_CHOICE" in
            3)
                ensure_ssh_key
                echo -e "${CYAN}  Pinggy 터널 시작 중... (아래에 외부 URL이 표시됩니다)${NC}"
                ssh -p443 -R0:localhost:${PORT} \
                    -i "$SSH_KEY" -o IdentitiesOnly=yes \
                    -o StrictHostKeyChecking=accept-new \
                    a.pinggy.io &
                ;;
            4)
                ensure_ssh_key
                echo -e "${CYAN}  localhost.run 터널 시작 중... (아래에 외부 URL이 표시됩니다)${NC}"
                ssh -R 80:localhost:${PORT} \
                    -i "$SSH_KEY" -o IdentitiesOnly=yes \
                    -o StrictHostKeyChecking=accept-new \
                    nokey@localhost.run &
                ;;
            5)
                ensure_cloudflared
                echo -e "${CYAN}  Cloudflare 터널 시작 중... (아래에 외부 URL이 표시됩니다)${NC}"
                "$CLOUDFLARED_PATH" tunnel --url "http://localhost:$PORT" &
                ;;
        esac
        TUNNEL_PID=$!

        while kill -0 "$PROXY_PID" 2>/dev/null && kill -0 "$TUNNEL_PID" 2>/dev/null; do
            sleep 1
        done
        ;;
    *)
        # ── 로컬 모드 (HTTP / HTTPS) ────────────────
        echo -e "${GRAY}  종료하려면 Ctrl+C를 누르세요.${NC}"
        cd "$PROJECT_DIR"
        "$EXE_PATH" || true
        ;;
esac
