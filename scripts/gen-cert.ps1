$ErrorActionPreference = 'Stop'

$dir      = Split-Path -Parent $MyInvocation.MyCommand.Path
$certFile = Join-Path $dir "cert.pem"
$keyFile  = Join-Path $dir "key.pem"
$crtFile  = Join-Path $dir "cert.crt"

# --- Generate RSA 2048-bit key pair ---
$rsa = [System.Security.Cryptography.RSA]::Create(2048)

# --- Build self-signed certificate ---
$req = [System.Security.Cryptography.X509Certificates.CertificateRequest]::new(
    "CN=localhost", $rsa,
    [System.Security.Cryptography.HashAlgorithmName]::SHA256,
    [System.Security.Cryptography.RSASignaturePadding]::Pkcs1
)

# SAN: localhost + 127.0.0.1 + ::1
$san = [System.Security.Cryptography.X509Certificates.SubjectAlternativeNameBuilder]::new()
$san.AddDnsName("localhost")
$san.AddIpAddress([System.Net.IPAddress]::Parse("127.0.0.1"))
$san.AddIpAddress([System.Net.IPAddress]::Parse("::1"))
$req.CertificateExtensions.Add($san.Build())

# Basic Constraints: not a CA
$req.CertificateExtensions.Add(
    [System.Security.Cryptography.X509Certificates.X509BasicConstraintsExtension]::new($false, $false, 0, $true)
)

# Key Usage: Digital Signature + Key Encipherment
$req.CertificateExtensions.Add(
    [System.Security.Cryptography.X509Certificates.X509KeyUsageExtension]::new(
        [System.Security.Cryptography.X509Certificates.X509KeyUsageFlags]::DigitalSignature -bor
        [System.Security.Cryptography.X509Certificates.X509KeyUsageFlags]::KeyEncipherment,
        $true
    )
)

# Enhanced Key Usage: Server Authentication
$oids = [System.Security.Cryptography.OidCollection]::new()
[void]$oids.Add([System.Security.Cryptography.Oid]::new("1.3.6.1.5.5.7.3.1"))
$req.CertificateExtensions.Add(
    [System.Security.Cryptography.X509Certificates.X509EnhancedKeyUsageExtension]::new($oids, $true)
)

# Self-sign (valid 10 years)
$notBefore = [System.DateTimeOffset]::Now
$notAfter  = $notBefore.AddYears(10)
$cert = $req.CreateSelfSigned($notBefore, $notAfter)

# --- Export certificate PEM ---
$certBase64 = [Convert]::ToBase64String($cert.RawData, 'InsertLineBreaks')
$certPem    = "-----BEGIN CERTIFICATE-----`r`n$certBase64`r`n-----END CERTIFICATE-----`r`n"
[System.IO.File]::WriteAllText($certFile, $certPem)

# --- Export certificate DER (.crt for Windows install) ---
[System.IO.File]::WriteAllBytes($crtFile, $cert.RawData)

# --- Export private key PEM (PKCS#8) ---
$cngKey   = ([System.Security.Cryptography.RSACng]$rsa).Key
$pkcs8    = $cngKey.Export([System.Security.Cryptography.CngKeyBlobFormat]::Pkcs8PrivateBlob)
$keyBase64 = [Convert]::ToBase64String($pkcs8, 'InsertLineBreaks')
$keyPem    = "-----BEGIN PRIVATE KEY-----`r`n$keyBase64`r`n-----END PRIVATE KEY-----`r`n"
[System.IO.File]::WriteAllText($keyFile, $keyPem)

$rsa.Dispose()

Write-Host ""
Write-Host "TLS certificate generated!" -ForegroundColor Green
Write-Host "  cert : $certFile"
Write-Host "  key  : $keyFile"
Write-Host "  crt  : $crtFile (double-click to install)"
Write-Host "  valid: $($notBefore.ToString('yyyy-MM-dd')) ~ $($notAfter.ToString('yyyy-MM-dd'))"
Write-Host "  SAN  : localhost, 127.0.0.1, ::1"
Write-Host ""
Write-Host "To trust this certificate on Windows:" -ForegroundColor Yellow
Write-Host "  1. Double-click cert.crt"
Write-Host "  2. Install Certificate -> Local Machine"
Write-Host "  3. Place in: Trusted Root Certification Authorities"
Write-Host ""
