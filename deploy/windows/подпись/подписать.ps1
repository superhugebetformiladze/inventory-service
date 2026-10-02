# Подпись собранного агента.
#
#   .\подписать.ps1 -Файл ..\..\..\build\пакет\agent.exe
#
# Выполняется после каждой сборки, до того как пакет попадёт в общую папку.
# Сертификат создаётся один раз скриптом создать-сертификат.ps1.

param(
    [Parameter(Mandatory = $true)]
    [string]$Файл,

    # Служба меток времени. Метка обязательна: без неё подпись перестанет
    # считаться действительной в день, когда истечёт сертификат, и агент,
    # спокойно работающий на пятистах машинах, вдруг станет неподписанным.
    # С меткой подпись говорит «файл был подписан тогда, когда сертификат
    # действовал», и остаётся годной.
    [string]$Метки = 'http://timestamp.digicert.com'
)

$ErrorActionPreference = 'Stop'
Set-Location -Path $PSScriptRoot

if (-not (Test-Path $Файл)) {
    Write-Host "Нет файла: $Файл" -ForegroundColor Red
    exit 1
}

$pfx = Join-Path $PSScriptRoot 'ключи\подпись.pfx'
if (-not (Test-Path $pfx)) {
    Write-Host 'Сертификата нет. Сначала выполните создать-сертификат.ps1' -ForegroundColor Red
    exit 1
}

$пароль = Read-Host -AsSecureString 'Пароль закрытого ключа'

# Сертификат читается через X509Certificate2, а не Get-PfxCertificate.
#
# У последнего параметр -Password появился только в PowerShell 6, а на машинах
# администраторов стоит Windows PowerShell 5.1, где вызов падает с «параметр
# не найден». Проверено на живой машине: первая версия этого скрипта именно
# так и не сработала.
$флаги = [System.Security.Cryptography.X509Certificates.X509KeyStorageFlags]::Exportable
$сертификат = New-Object System.Security.Cryptography.X509Certificates.X509Certificate2(
    $pfx, $пароль, $флаги)

$результат = Set-AuthenticodeSignature `
    -FilePath $Файл `
    -Certificate $сертификат `
    -HashAlgorithm SHA256 `
    -TimestampServer $Метки

# Успех — это поставленная подпись с меткой времени, а НЕ статус Valid.
#
# Сразу после подписи статус на машине сборки — UnknownError с пояснением
# «цепочка завершилась в недоверенном корневом сертификате», и это правильное
# состояние: сертификат самоподписанный, а доверие к нему появляется только
# там, где его установили, — то есть на рабочих станциях, пакетом раскатки.
# Первая версия этого скрипта считала такой ответ провалом и объявляла
# неудачей нормально подписанный файл.
$подписано = $null -ne $результат.SignerCertificate
$сМеткой = $null -ne $результат.TimeStamperCertificate

if (-not $подписано) {
    Write-Host "Подписать не удалось: $($результат.Status) — $($результат.StatusMessage)" -ForegroundColor Red
    exit 1
}

if (-not $сМеткой) {
    # Без метки подпись перестанет считаться действительной в день, когда
    # истечёт сертификат, и агент, спокойно работающий на пятистах машинах,
    # вдруг станет неподписанным. Лучше не подписать сейчас, чем обнаружить
    # это через пять лет.
    Write-Host 'Подпись поставлена, но БЕЗ метки времени — так оставлять нельзя.' -ForegroundColor Red
    Write-Host 'Похоже, недоступна служба меток. Проверьте выход в интернет'
    Write-Host 'или укажите другую: -Метки http://timestamp.sectigo.com'
    exit 1
}

Write-Host ''
Write-Host "Подписано: $Файл" -ForegroundColor Green
Write-Host "Кем:       $($сертификат.Subject)"
Write-Host "Отпечаток: $($сертификат.Thumbprint)"
Write-Host ''
Write-Host 'Проверить на рабочей станции, где пакет уже установлен (статус Valid):'
Write-Host "  Get-AuthenticodeSignature 'C:\Program Files\inventory-agent\agent.exe'"
Write-Host ''
Write-Host 'Здесь, на машине сборки, статус остаётся UnknownError «недоверенный'
Write-Host 'корень»: доверие к своему сертификату появляется только там, где его'
Write-Host 'установили пакетом раскатки.'
