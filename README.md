# tiny-proxy

경량 HTTP/HTTPS 리버스 프록시. 헤더 포워딩, 인증, 호스트 화이트리스트를 지원합니다.

## 빠른 시작

```bash
go build -o tiny-proxy .
./tiny-proxy
```

기본값으로 `http://localhost:8080`에서 실행됩니다.

## 설정

`.env.example`을 `.env`로 복사한 뒤 필요한 값을 수정합니다.

```bash
cp .env.example .env
```

| 변수 | 기본값 | 설명 |
|---|---|---|
| `PORT` | `8080` | 수신 포트 |
| `TIMEOUT` | `30s` | 업스트림 요청 타임아웃 |
| `TLS_CERT` | | TLS 인증서 파일 경로 |
| `TLS_KEY` | | TLS 개인키 파일 경로 |
| `AUTH_MODE` | `none` | 인증 모드: `none`, `key`, `hash` |
| `AUTH_KEY` | | 정적 API 키 (`key` 모드) |
| `AUTH_SECRET` | | HMAC 시크릿 (`hash` 모드) |
| `AUTH_WINDOW` | `30` | HMAC 시간 윈도우 (초) |
| `WHITELIST` | | 허용 호스트 (쉼표 구분, `*` 와일드카드 지원) |
| `HEADER_URL` | `X-Proxy-Url` | 대상 URL을 지정하는 헤더 이름 |
| `HEADER_PREFIX` | `X-Proxy-H-` | 대상 서버로 전달할 헤더 접두사 |
| `HEADER_BULK` | `X-Proxy-Headers` | JSON으로 여러 헤더를 전달하는 헤더 이름 |
| `HEADER_AUTH` | `X-Proxy-Auth` | 프록시 인증 토큰 헤더 이름 |

## HTTPS 설정

### 1. 인증서 생성

`scripts/gen-cert.bat`을 더블클릭하거나 터미널에서 실행합니다.

```bash
scripts/gen-cert.bat
```

`scripts/` 폴더에 다음 파일이 생성됩니다:

| 파일 | 용도 |
|---|---|
| `cert.pem` | 서버용 인증서 (PEM) |
| `key.pem` | 서버용 개인키 (PEM) |
| `cert.crt` | Windows 설치용 인증서 (DER) |

### 2. .env 설정

```
TLS_CERT=scripts/cert.pem
TLS_KEY=scripts/key.pem
```

### 3. 인증서 설치 (선택)

브라우저에서 경고 없이 접속하려면 인증서를 Windows에 설치합니다.

1. `scripts/cert.crt` 더블클릭
2. **인증서 설치** 클릭
3. 저장소 위치: **로컬 컴퓨터** 선택
4. **모든 인증서를 다음 저장소에 저장** 선택 → **신뢰할 수 있는 루트 인증 기관**
5. 완료

설치 후 Chrome, Edge 등에서 `https://localhost:8080/_health`에 경고 없이 접속 가능합니다.

## 사용법

`X-Proxy-Url` 헤더에 대상 URL을 지정합니다:

```bash
curl -H "X-Proxy-Url: https://example.com/api" http://localhost:8080
```

`X-Proxy-H-` 접두사로 대상 서버에 헤더를 전달합니다:

```bash
curl \
  -H "X-Proxy-Url: https://api.example.com/data" \
  -H "X-Proxy-H-Authorization: Bearer token123" \
  http://localhost:8080
```

`X-Proxy-Headers`로 여러 헤더를 JSON으로 한 번에 전달합니다:

```bash
curl \
  -H "X-Proxy-Url: https://api.example.com/data" \
  -H 'X-Proxy-Headers: %7B%22Authorization%22%3A%22Bearer%20token%22%7D' \
  http://localhost:8080
```

헬스체크: `GET /_health`
