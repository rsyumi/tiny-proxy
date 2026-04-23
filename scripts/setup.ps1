$ErrorActionPreference = 'Stop'
$ProgressPreference    = 'SilentlyContinue'

# ── 경로 설정 ────────────────────────────────────────
$scriptDir  = Split-Path -Parent $MyInvocation.MyCommand.Path
$projectDir = Split-Path -Parent $scriptDir

if (-not (Test-Path (Join-Path $projectDir 'main.go'))) {
    Write-Host "ERROR: tiny-proxy 프로젝트의 scripts/ 폴더 안에서 실행해야 합니다." -ForegroundColor Red
    exit 1
}

$owner      = 'rsyumi'
$repo       = 'tiny-proxy'
$exePath    = Join-Path $projectDir 'tiny-proxy.exe'
$envFile    = Join-Path $projectDir '.env'
$envExample = Join-Path $projectDir '.env.example'

# ── 방향키 메뉴 함수 ─────────────────────────────────
function Show-Menu {
    param([string]$Title, [string[]]$Options)

    Write-Host ""
    Write-Host "  $Title  " -ForegroundColor White -NoNewline
    Write-Host "(Up/Down, Enter)" -ForegroundColor DarkGray
    Write-Host ""

    $sel = 0
    $top = [Console]::CursorTop

    while ($true) {
        [Console]::SetCursorPosition(0, $top)
        for ($i = 0; $i -lt $Options.Count; $i++) {
            if ($i -eq $sel) {
                $text = "  > $($Options[$i])"
            } else {
                $text = "    $($Options[$i])"
            }
            $displayWidth = 0
            foreach ($c in $text.ToCharArray()) {
                if ([int]$c -gt 0x7F) { $displayWidth += 2 } else { $displayWidth += 1 }
            }
            $pad = [Math]::Max(0, [Console]::WindowWidth - $displayWidth - 1)
            $color = if ($i -eq $sel) { 'Cyan' } else { 'Gray' }
            Write-Host "$text$(' ' * $pad)" -ForegroundColor $color
        }

        $key = [Console]::ReadKey($true)
        switch ($key.Key) {
            'UpArrow'   { if ($sel -gt 0) { $sel-- } }
            'DownArrow' { if ($sel -lt $Options.Count - 1) { $sel++ } }
            'Enter'     { Write-Host ""; return $sel }
        }
    }
}

# ── .env 값 설정 헬퍼 ────────────────────────────────
function Set-EnvValue {
    param([string[]]$Lines, [string]$Key, [string]$Value)

    $found  = $false
    $result = @()
    foreach ($line in $Lines) {
        if ($line -match "^\s*#?\s*${Key}\s*=") {
            $result += "${Key}=${Value}"
            $found = $true
        } else {
            $result += $line
        }
    }
    if (-not $found) { $result += "${Key}=${Value}" }
    return ,$result
}

# ── 배너 ─────────────────────────────────────────────
Clear-Host
Write-Host ""
Write-Host "  ==========================================" -ForegroundColor Cyan
Write-Host "    tiny-proxy  One-Click Setup" -ForegroundColor Cyan
Write-Host "  ==========================================" -ForegroundColor Cyan

# ══════════════════════════════════════════════════════
#  사용자 선택
# ══════════════════════════════════════════════════════

# ── Q1: 모드 선택 ────────────────────────────────────
$mode = Show-Menu "사용 방식을 선택하세요" @(
    "로컬 HTTPS 인증서 발급   (이 컴퓨터의 브라우저에서만 사용)"
    "외부 터널 생성            (모바일 등 외부 기기에서 접속)"
)

# ── Q2: 비밀번호 ─────────────────────────────────────
$existingKey = ''
if (Test-Path $envFile) {
    foreach ($line in (Get-Content $envFile)) {
        if ($line -match '^\s*AUTH_KEY\s*=\s*(.+)') {
            $existingKey = $Matches[1].Trim()
            break
        }
    }
}

$authKey = ''
if ($existingKey) {
    $masked = $existingKey.Substring(0, [Math]::Min(2, $existingKey.Length)) + ('*' * [Math]::Max(0, $existingKey.Length - 2))
    $useAuth = Show-Menu "비밀번호를 사용하시겠습니까?" @(
        "기존 유지   ($masked)"
        "새로 입력   (새 비밀번호 설정)"
        "아니요      (인증 없이 사용)"
    )
    if ($useAuth -eq 0) {
        $authKey = $existingKey
    } elseif ($useAuth -eq 1) {
        Write-Host ""
        $authKey = Read-Host "  비밀번호를 입력하세요"
        Write-Host "  입력된 비밀번호: $authKey" -ForegroundColor Green
    }
} else {
    $useAuth = Show-Menu "비밀번호를 사용하시겠습니까?" @(
        "아니요   (인증 없이 사용)"
        "예       (API 키 인증)"
    )
    if ($useAuth -eq 1) {
        Write-Host ""
        $authKey = Read-Host "  비밀번호를 입력하세요"
        Write-Host "  입력된 비밀번호: $authKey" -ForegroundColor Green
    }
}

# ── Q3: 터널 제공자 (터널 모드일 때만) ────────────────
$tunnelProvider = -1
if ($mode -eq 1) {
    $tunnelProvider = Show-Menu "터널 서비스를 선택하세요" @(
        "Pinggy              (무료, 60분 제한, 설치 불필요)"
        "localhost.run       (무료, 제한 없음, 설치 불필요)"
        "ngrok               (직접 설치 및 로그인 필요)"
        "cloudflare tunnel   (직접 설치 필요, 로그인 불필요)"
    )
}

# ══════════════════════════════════════════════════════
#  자동 설정
# ══════════════════════════════════════════════════════

Write-Host ""
Write-Host "  ------------------------------------------" -ForegroundColor DarkGray
Write-Host ""

# ── 1. 최신 바이너리 다운로드 ────────────────────────
Write-Host "  [1/3] GitHub Releases에서 최신 바이너리 다운로드 중..." -ForegroundColor Yellow

try {
    $headers    = @{ 'User-Agent' = 'tiny-proxy-setup' }
    $releaseUrl = "https://api.github.com/repos/$owner/$repo/releases/latest"
    $release    = Invoke-RestMethod -Uri $releaseUrl -Headers $headers

    $asset = $release.assets |
             Where-Object { $_.name -match 'windows.*amd64' } |
             Select-Object -First 1

    if ($asset) {
        $sizeMB = '{0:N2}' -f ($asset.size / 1MB)
        Write-Host "    $($asset.name) ($sizeMB MB)" -ForegroundColor Green
        Invoke-WebRequest -Uri $asset.browser_download_url `
                          -OutFile $exePath `
                          -Headers $headers `
                          -UseBasicParsing
        Write-Host "    다운로드 완료" -ForegroundColor Green
    } else {
        Write-Host "    WARNING: Windows AMD64 바이너리를 찾을 수 없습니다." -ForegroundColor Red
        if (-not (Test-Path $exePath)) {
            Write-Host "    go build -o tiny-proxy.exe . 로 직접 빌드하세요." -ForegroundColor Red
            exit 1
        }
        Write-Host "    기존 바이너리를 사용합니다." -ForegroundColor Yellow
    }
} catch {
    Write-Host "    WARNING: 릴리스 정보를 가져올 수 없습니다." -ForegroundColor Red
    Write-Host "    $($_.Exception.Message)" -ForegroundColor DarkGray
    if (-not (Test-Path $exePath)) {
        Write-Host "    바이너리가 없습니다. 직접 빌드하세요." -ForegroundColor Red
        exit 1
    }
    Write-Host "    기존 바이너리를 사용합니다." -ForegroundColor Yellow
}

# ── 2. .env 파일 설정 ────────────────────────────────
Write-Host ""
Write-Host "  [2/3] .env 파일 설정 중..." -ForegroundColor Yellow

if (Test-Path $envFile) {
    $envLines = @(Get-Content $envFile)
} elseif (Test-Path $envExample) {
    $envLines = @(Get-Content $envExample)
} else {
    $envLines = @(
        'PORT=8080'
        'TIMEOUT=30m'
        'AUTH_MODE=none'
        'HEADER_URL=Yumi-Url'
        'HEADER_PREFIX=Yumi-H-'
        'HEADER_BULK=Yumi-Headers'
        'HEADER_AUTH=Yumi-Auth'
    )
}

if ($mode -eq 0) {
    $envLines = Set-EnvValue $envLines 'TLS_CERT' 'scripts/cert.pem'
    $envLines = Set-EnvValue $envLines 'TLS_KEY'  'scripts/key.pem'
} else {
    $envLines = Set-EnvValue $envLines 'TLS_CERT' ''
    $envLines = Set-EnvValue $envLines 'TLS_KEY'  ''
}

if ($authKey) {
    $envLines = Set-EnvValue $envLines 'AUTH_MODE' 'key'
    $envLines = Set-EnvValue $envLines 'AUTH_KEY'  $authKey
} else {
    $envLines = Set-EnvValue $envLines 'AUTH_MODE' 'none'
    $envLines = Set-EnvValue $envLines 'AUTH_KEY'  ''
}

$envLines | Set-Content $envFile
Write-Host "    .env 생성 완료" -ForegroundColor Green

# ── 3. TLS 인증서 (로컬 HTTPS 모드만) ───────────────
Write-Host ""
if ($mode -eq 0) {
    $certFile = Join-Path $scriptDir 'cert.pem'
    $keyFile  = Join-Path $scriptDir 'key.pem'

    if (-not (Test-Path $certFile) -or -not (Test-Path $keyFile)) {
        Write-Host "  [3/3] TLS 인증서 생성 중..." -ForegroundColor Yellow

        $genCert = Join-Path $scriptDir 'gen-cert.ps1'
        if (-not (Test-Path $genCert)) {
            Write-Host ""
            Write-Host "    ERROR: gen-cert.ps1을 찾을 수 없습니다." -ForegroundColor Red
            Write-Host "    필요한 파일: $genCert" -ForegroundColor Red
            Write-Host "    프로젝트의 scripts/ 폴더에 gen-cert.ps1이 포함되어 있는지 확인하세요." -ForegroundColor Yellow
            exit 1
        }
        & $genCert

        # 인증서 설치 여부 확인
        $crtFile = Join-Path $scriptDir 'cert.crt'
        if (Test-Path $crtFile) {
            $installCert = Show-Menu "인증서를 Windows에 설치하시겠습니까?" @(
                "예   (브라우저에서 HTTPS 경고 없이 접속 가능, 관리자 권한 필요)"
                "아니요   (나중에 scripts\cert.crt를 더블클릭하여 수동 설치 가능)"
            )

            if ($installCert -eq 0) {
                Write-Host "    관리자 권한을 요청합니다 (UAC 팝업에서 '예'를 눌러주세요)..." -ForegroundColor Yellow
                $proc = Start-Process certutil `
                    -ArgumentList '-addstore','Root',"`"$crtFile`"" `
                    -Verb RunAs -Wait -PassThru 2>$null

                if ($proc -and $proc.ExitCode -eq 0) {
                    Write-Host "    인증서 설치 완료! 브라우저에서 경고 없이 접속할 수 있습니다." -ForegroundColor Green
                } else {
                    Write-Host "    인증서 설치를 건너뛰었습니다." -ForegroundColor Yellow
                    Write-Host "    나중에 scripts\cert.crt를 더블클릭하여 수동 설치할 수 있습니다." -ForegroundColor DarkGray
                }
            }
        }
    } else {
        Write-Host "  [3/3] TLS 인증서가 이미 존재합니다." -ForegroundColor DarkGray
    }
} else {
    Write-Host "  [3/3] 터널 모드: TLS 인증서 불필요. 건너뜁니다." -ForegroundColor DarkGray
}

# ══════════════════════════════════════════════════════
#  실행
# ══════════════════════════════════════════════════════

$port = '8080'
foreach ($line in (Get-Content $envFile)) {
    if ($line -match '^\s*PORT\s*=\s*(.+)') {
        $port = $Matches[1].Trim()
        break
    }
}

Write-Host ""

if ($mode -eq 0) {
    # ── 로컬 HTTPS 모드 ──────────────────────────────
    Write-Host "  ==========================================" -ForegroundColor Green
    Write-Host "    tiny-proxy 실행 중" -ForegroundColor Green
    Write-Host "    https://localhost:$port" -ForegroundColor Green
    Write-Host "  ==========================================" -ForegroundColor Green
    Write-Host ""

    Push-Location $projectDir
    try { & $exePath } finally { Pop-Location }

} else {
    # ── 터널 모드 ─────────────────────────────────────
    Write-Host "  ==========================================" -ForegroundColor Green
    Write-Host "    tiny-proxy + 터널 시작 중..." -ForegroundColor Green
    Write-Host "    http://localhost:$port" -ForegroundColor Green
    Write-Host "  ==========================================" -ForegroundColor Green
    Write-Host ""

    # 터널 무인증 경고
    if (-not $authKey) {
        Write-Host "  ※ 주의: 인증 없이 터널을 열면 누구나 이 프록시를 사용할 수 있습니다." -ForegroundColor Red
        Write-Host "    .env에서 AUTH_MODE=key / AUTH_KEY=<비밀번호>를 설정하는 것을 권장합니다." -ForegroundColor Red
    }

    # 프로젝트 전용 SSH 키 (사용자의 ~/.ssh 키와 분리)
    $sshKey = Join-Path $scriptDir '.ssh_key'
    if ($tunnelProvider -le 1 -and -not (Test-Path $sshKey)) {
        if (-not (Get-Command ssh-keygen -ErrorAction SilentlyContinue)) {
            Write-Host "  ERROR: ssh-keygen을 찾을 수 없습니다. OpenSSH를 설치해주세요." -ForegroundColor Red
            exit 1
        }
        Write-Host "  터널용 SSH 키 생성 중..." -ForegroundColor Yellow
        ssh-keygen -t ed25519 -f $sshKey -N "" -q
        Write-Host "    SSH 키 생성 완료: $sshKey" -ForegroundColor Green
    }

    # 터널 명령 결정
    $tunnelCmd  = $null
    $tunnelArgs = $null
    $providerNames = @('Pinggy', 'localhost.run', 'ngrok', 'cloudflare tunnel')

    switch ($tunnelProvider) {
        0 {
            if (-not (Get-Command ssh -ErrorAction SilentlyContinue)) {
                Write-Host "  ERROR: ssh가 설치되어 있지 않습니다." -ForegroundColor Red
                Write-Host "  Windows 설정 > 앱 > 선택적 기능 > OpenSSH 클라이언트 설치" -ForegroundColor Yellow
                exit 1
            }
            $tunnelCmd  = 'ssh'
            $tunnelArgs = "-p443 -R0:localhost:${port} -i `"$sshKey`" -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new a.pinggy.io"
        }
        1 {
            if (-not (Get-Command ssh -ErrorAction SilentlyContinue)) {
                Write-Host "  ERROR: ssh가 설치되어 있지 않습니다." -ForegroundColor Red
                Write-Host "  Windows 설정 > 앱 > 선택적 기능 > OpenSSH 클라이언트 설치" -ForegroundColor Yellow
                exit 1
            }
            $tunnelCmd  = 'ssh'
            $tunnelArgs = "-R 80:localhost:${port} -i `"$sshKey`" -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new nokey@localhost.run"
        }
        2 {
            if (-not (Get-Command ngrok -ErrorAction SilentlyContinue)) {
                Write-Host "  ERROR: ngrok이 설치되어 있지 않습니다." -ForegroundColor Red
                Write-Host "  https://ngrok.com/download 에서 설치한 뒤" -ForegroundColor Yellow
                Write-Host "  ngrok config add-authtoken <TOKEN> 으로 로그인 후 다시 시도하세요." -ForegroundColor Yellow
                exit 1
            }
            $tunnelCmd  = 'ngrok'
            $tunnelArgs = "http $port"
        }
        3 {
            if (-not (Get-Command cloudflared -ErrorAction SilentlyContinue)) {
                Write-Host "  ERROR: cloudflared가 설치되어 있지 않습니다." -ForegroundColor Red
                Write-Host "  https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/" -ForegroundColor Yellow
                Write-Host "  에서 설치 후 다시 시도하세요." -ForegroundColor Yellow
                exit 1
            }
            $tunnelCmd  = 'cloudflared'
            $tunnelArgs = "tunnel --url http://localhost:$port"
        }
    }

    # 프록시와 터널을 함께 실행, 하나가 종료되면 나머지도 종료
    $proxyProc  = $null
    $tunnelProc = $null

    try {
        $proxyProc = Start-Process -FilePath $exePath -WorkingDirectory $projectDir -PassThru
        Start-Sleep -Seconds 2
        $tunnelProc = Start-Process -FilePath $tunnelCmd -ArgumentList $tunnelArgs -PassThru -NoNewWindow

        Write-Host "  터널 시작: $($providerNames[$tunnelProvider])" -ForegroundColor Cyan
        Write-Host "  아래에 외부 접속 URL이 표시됩니다." -ForegroundColor White
        Write-Host "  종료하려면 Ctrl+C를 누르세요." -ForegroundColor DarkGray
        Write-Host ""

        while (-not $proxyProc.HasExited -and -not $tunnelProc.HasExited) {
            Start-Sleep -Milliseconds 500
        }
    } finally {
        if ($proxyProc  -and -not $proxyProc.HasExited)  { $proxyProc.Kill() }
        if ($tunnelProc -and -not $tunnelProc.HasExited) { $tunnelProc.Kill() }
        Write-Host ""
        Write-Host "  프록시와 터널이 종료되었습니다." -ForegroundColor Yellow
    }
}
