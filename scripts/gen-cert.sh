#!/usr/bin/env bash
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
CERT_FILE="$SCRIPT_DIR/cert.pem"
KEY_FILE="$SCRIPT_DIR/key.pem"
CRT_FILE="$SCRIPT_DIR/cert.crt"

# ── openssl 설치 확인 ────────────────────────────────
if ! command -v openssl &>/dev/null; then
    echo "openssl 설치 중..."
    if command -v pkg &>/dev/null; then
        pkg install -y openssl-tool
    elif command -v apt &>/dev/null; then
        apt install -y openssl
    else
        echo "ERROR: openssl을 설치할 수 없습니다. 직접 설치해주세요."
        exit 1
    fi
fi

# ── 인증서 생성 ──────────────────────────────────────
openssl req -x509 -newkey rsa:2048 \
    -keyout "$KEY_FILE" -out "$CERT_FILE" \
    -days 3650 -nodes \
    -subj "/CN=localhost" \
    -addext "subjectAltName=DNS:localhost,IP:127.0.0.1,IP:::1" \
    -addext "basicConstraints=CA:FALSE" \
    -addext "keyUsage=digitalSignature,keyEncipherment" \
    -addext "extendedKeyUsage=serverAuth" \
    2>&1

# ── DER 형식 변환 (.crt) ────────────────────────────
openssl x509 -in "$CERT_FILE" -outform DER -out "$CRT_FILE"

# ── 유효기간 ─────────────────────────────────────────
NOT_BEFORE="$(date +%Y-%m-%d)"
NOT_AFTER="$(date -d '+10 years' +%Y-%m-%d 2>/dev/null || date -v+10y +%Y-%m-%d 2>/dev/null || echo '~10년')"

echo ""
echo -e "\033[0;32mTLS certificate generated!\033[0m"
echo "  cert : $CERT_FILE"
echo "  key  : $KEY_FILE"
echo "  crt  : $CRT_FILE"
echo "  valid: $NOT_BEFORE ~ $NOT_AFTER"
echo "  SAN  : localhost, 127.0.0.1, ::1"
echo ""
