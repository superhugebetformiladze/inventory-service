# Обёртка над Makefile для Windows, где make не установлен.
#
# Цели повторяют Makefile один в один: make.ps1 dev == make dev. Держать две
# реализации плохо, но альтернатива — требовать установки make на каждой
# машине разработчика, а в CI и на сервере используется сам Makefile.
#
#   .\make.ps1 dev      поднять окружение
#   .\make.ps1 ci       формат, vet, линтер, тесты
#   .\make.ps1 help     список целей

param(
    [Parameter(Position = 0)]
    [string]$Target = 'help',

    # Остаток аргументов уходит в agentctl как есть:
    #   .\make.ps1 token -label "раскатка" -days 14 -uses 600
    #   .\make.ps1 revoke -agent <отметка> -reason "списана"
    [Parameter(ValueFromRemainingArguments = $true)]
    [string[]]$Rest = @()
)

$ErrorActionPreference = 'Stop'
Set-Location -Path $PSScriptRoot

$compose = @('compose', '--env-file', '.env', '-f', 'deploy/docker-compose.yml')
$prod = @('compose', '--env-file', '.env.prod', '-f', 'deploy/docker-compose.prod.yml')
# Разовые команды не поднимают зависимости: линтеру база не нужна.
$run = $compose + @('run', '--rm', '--no-deps', 'server')

function Invoke-Docker {
    param([string[]]$Arguments)
    & docker @Arguments
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}

function Ensure-Env {
    if (-not (Test-Path '.env')) {
        Copy-Item '.env.example' '.env'
        Write-Host 'Создан .env — задайте в нём POSTGRES_PASSWORD' -ForegroundColor Yellow
    }
}

switch ($Target) {
    'help' {
        Write-Host ''
        Write-Host '  dev       поднять окружение разработки'
        Write-Host '  restart   перезапустить службу после правки кода'
        Write-Host '  down      остановить'
        Write-Host '  clean     остановить и удалить тома (данные потеряются)'
        Write-Host '  status    состояние сервисов'
        Write-Host '  logs      журнал службы'
        Write-Host '  test      тесты'
        Write-Host '  cover     тесты с покрытием'
        Write-Host '  lint      линтер'
        Write-Host '  fmt       форматирование'
        Write-Host '  vet       проверки компилятора'
        Write-Host '  tidy      привести go.mod в порядок'
        Write-Host '  migrate   накатить миграции базы'
        Write-Host '  topics    создать топики ленты'
        Write-Host '  token     создать токен регистрации агентов'
        Write-Host '  agents    список агентов: кто на связи, кто отозван'
        Write-Host '  collect   собрать сейчас: -host <имя компьютера>'
        Write-Host '  tasks     очередь заданий и их состояние'
        Write-Host '  pending   очередь разбора: неопознанные машины'
        Write-Host '  link      связать отложенный отчёт с машиной'
        Write-Host '  import    принять отчёты с машин без сети: -dir <каталог>'
        Write-Host '  agent     собрать комплект раскатки под Windows в build/'
        Write-Host ''
        Write-Host '  netscan-import  принять снимки коммутаторов из файлов: -dir <каталог>'
        Write-Host '  netscan-poll    опросить коммутатор сейчас: -target <адрес> [-dry]'
        Write-Host '  netscan-list    опрошенные сетевые устройства'
        Write-Host '  netscan-exe     собрать netscan.exe для машины с доступом к коммутаторам'
        Write-Host '  package   собрать пакет раскатки в build/пакет/ (кладётся в общую папку)'
        Write-Host ''
        Write-Host '  prod-build / prod-migrate / prod-up / prod-down / prod-ps / prod-logs'
        Write-Host '  prod-agents  список агентов на рабочем сервере'
        Write-Host '  prod-ctl     любая команда agentctl: .\make.ps1 prod-ctl token -label ... -days 14 -uses 600'
        Write-Host '  demo      сквозной путь: регистрация -> отчёт -> лента'
        Write-Host '  build     собрать образ службы'
        Write-Host '  ci        формат, vet, линтер, тесты'
        Write-Host '  smoke     проверить живой стек'
        Write-Host ''
    }
    'dev' {
        Ensure-Env
        Invoke-Docker ($compose + @('up', '-d', '--build'))
    }
    'restart' { Invoke-Docker ($compose + @('restart', 'server')) }
    'down'   { Invoke-Docker ($compose + @('down')) }
    'clean'  { Invoke-Docker ($compose + @('down', '-v')) }
    'status' { Invoke-Docker ($compose + @('ps')) }
    'logs'   { Invoke-Docker ($compose + @('logs', '-f', 'server')) }
    'test'   { Invoke-Docker ($run + @('go', 'test', './...', '-race', '-count=1')) }
    'cover'  { Invoke-Docker ($run + @('sh', '-c', 'go test ./... -coverprofile=/tmp/c.out && go tool cover -func=/tmp/c.out | tail -1')) }
    'lint'   { Invoke-Docker ($run + @('golangci-lint', 'run', './...')) }
    'fmt'    { Invoke-Docker ($run + @('gofmt', '-w', '-l', '.')) }
    'vet'    { Invoke-Docker ($run + @('go', 'vet', './...')) }
    'tidy'   { Invoke-Docker ($run + @('go', 'mod', 'tidy')) }
    'migrate' { Invoke-Docker ($compose + @('run', '--rm', 'server', 'go', 'run', './cmd/migrate')) }
    'topics' {
        foreach ($t in @(
            @('inventory.snapshots.v1', 'retention.ms=2592000000'),
            @('inventory.hosts.v1',     'cleanup.policy=compact'),
            @('inventory.events.v1',    'retention.ms=7776000000'),
            @('inventory.network.v1',   'cleanup.policy=compact'))) {
            & docker @($compose + @('exec', 'redpanda', 'rpk', 'topic', 'create', $t[0], '-p', '3', '-c', $t[1]))
        }
        Invoke-Docker ($compose + @('exec', 'redpanda', 'rpk', 'topic', 'list'))
    }
    'token'  { Invoke-Docker ($compose + @('run', '--rm', 'server', 'go', 'run', './cmd/agentctl', 'token') + $Rest) }
    'agents' { Invoke-Docker ($compose + @('run', '--rm', 'server', 'go', 'run', './cmd/agentctl', 'list')) }
    'revoke'  { Invoke-Docker ($compose + @('run', '--rm', 'server', 'go', 'run', './cmd/agentctl', 'revoke') + $Rest) }
    'collect' { Invoke-Docker ($compose + @('run', '--rm', 'server', 'go', 'run', './cmd/agentctl', 'collect') + $Rest) }
    'tasks'   { Invoke-Docker ($compose + @('run', '--rm', 'server', 'go', 'run', './cmd/agentctl', 'commands')) }
    'pending' { Invoke-Docker ($compose + @('run', '--rm', 'server', 'go', 'run', './cmd/agentctl', 'pending')) }
    'link'    { Invoke-Docker ($compose + @('run', '--rm', 'server', 'go', 'run', './cmd/agentctl', 'link') + $Rest) }
    'import' {
        # Каталог с флешки монтируется внутрь контейнера: сама флешка службе
        # недоступна, а разбирать файлы должен тот, у кого есть база.
        $dir = ($Rest | Select-Object -Last 1)
        if (-not $dir -or -not (Test-Path $dir)) {
            Write-Host 'Укажите каталог: .\make.ps1 import -dir X:\отчёты' -ForegroundColor Red
            exit 1
        }
        $full = (Resolve-Path $dir).Path
        Invoke-Docker ($compose + @('run', '--rm', '-v', "${full}:/mnt/offline:ro", 'server',
            'go', 'run', './cmd/agentctl', 'import', '-dir', '/mnt/offline'))
    }
    'netscan-import' {
        $dir = ($Rest | Select-Object -Last 1)
        if (-not $dir -or -not (Test-Path $dir)) {
            Write-Host 'Укажите каталог: .\make.ps1 netscan-import -dir X:\обходы' -ForegroundColor Red
            exit 1
        }
        $full = (Resolve-Path $dir).Path
        Invoke-Docker ($compose + @('run', '--rm', '-v', "${full}:/mnt/netscan:ro", 'server',
            'go', 'run', './cmd/agentctl', 'netscan-import', '-dir', '/mnt/netscan'))
    }
    'netscan-poll' { Invoke-Docker ($compose + @('run', '--rm', 'server', 'go', 'run', './cmd/agentctl', 'netscan-poll') + $Rest) }
    'netscan-list' { Invoke-Docker ($compose + @('run', '--rm', 'server', 'go', 'run', './cmd/agentctl', 'netscan-list')) }
    'netscan-exe' {
        Invoke-Docker ($run + @('sh', '-c',
            'GOOS=windows GOARCH=amd64 go build -trimpath -ldflags=''-s -w'' -o build/netscan.exe ./cmd/netscan'))
        Copy-Item 'scripts/switches.example.txt' 'build/' -Force
        Get-Item 'build/netscan.exe' | Select-Object Name, Length, LastWriteTime
    }
    'agent' {
        # Кросс-сборка под Windows идёт в контейнере: версия Go и флаги должны
        # быть теми же, что в CI, а не теми, что случайно стоят на машине.
        # Одинарные кавычки вокруг ldflags, а не двойные: двойные PowerShell
        # разбирает сам, и аргумент доезжает до go разорванным по пробелу.
        Invoke-Docker ($run + @('sh', '-c',
            'GOOS=windows GOARCH=amd64 go build -trimpath -ldflags=''-s -w'' -o build/agent.exe ./cmd/agent'))
        # Рядом с exe кладутся установщик и деинсталлятор: комплект
        # раскатки — это папка целиком, а не один файл.
        Copy-Item 'deploy/windows/*.cmd' 'build/' -Force
        Get-ChildItem 'build' | Select-Object Name, Length, LastWriteTime
    }

    'package' {
        # Пакет раскатки — это папка целиком, которую кладут в общую сетевую
        # папку: программа, скрипты, версия и место для токена.
        & $PSCommandPath 'agent'
        if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

        $dir = 'build/пакет'
        if (Test-Path $dir) { Remove-Item $dir -Recurse -Force }
        New-Item -ItemType Directory $dir | Out-Null

        Copy-Item 'build/agent.exe' $dir
        Copy-Item 'deploy/windows/*.cmd' $dir
        Copy-Item 'deploy/windows/ЧИТАТЬ.txt' $dir

        # Версия файлом, а не запуском agent.exe со share: на машинах с
        # запретом запуска программ из сети скрипт раскатки иначе не смог бы
        # сравнить версии и обновлял бы всё подряд на каждой загрузке.
        $version = (& docker @($run + @('go', 'run', './cmd/agent', 'version')) | Select-Object -Last 1).Trim()
        if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
        [System.IO.File]::WriteAllText((Join-Path $dir 'version.txt'), $version)
        [System.IO.File]::WriteAllText((Join-Path $dir 'token.txt'), '')
        # Адрес службы одним файлом на весь пакет: держать его в двух скриптах
        # значит однажды поправить один и раскатать половину парка не туда.
        [System.IO.File]::WriteAllText((Join-Path $dir 'server.txt'), 'http://inventory:8080')

        Write-Host "пакет собран: $dir (версия $version)"
        Write-Host 'положите в него токен раскатки: .\make.ps1 token -label "раскатка" -days 14 -uses 600'
        Get-ChildItem $dir | Select-Object Name, Length
    }
    'demo' {
        # Токен создаётся тут же: симулятор проходит тот же путь, что настоящий
        # агент, — без ключа отчёт не примут.
        #
        # Два отчёта с каждой машины, а не один. Первый идёт путём новой
        # машины, второй — путём уже известной, и это разный код.
        $out = & docker @($compose + @('run', '--rm', '-T', 'server', 'go', 'run', './cmd/agentctl', 'token', '-label', 'demo', '-days', '1', '-uses', '100'))
        $token = ($out | Where-Object { $_ -like 'enr_*' } | Select-Object -Last 1).Trim()
        if (-not $token) { Write-Host 'не удалось создать токен регистрации' -ForegroundColor Red; exit 1 }

        Invoke-Docker ($compose + @('run', '--rm', '--no-deps', 'server', 'go', 'run', './cmd/simulator', '-addr', 'http://server:8080', '-token', $token, '-hosts', '3', '-rounds', '2', '-v'))
        Start-Sleep -Seconds 3
        Invoke-Docker ($compose + @('exec', 'redpanda', 'rpk', 'topic', 'consume', 'inventory.snapshots.v1', '-n', '3', '-o', 'start', '-f', '%k -> %v`n'))
    }
    'build'  { Invoke-Docker @('build', '-f', 'deploy/Dockerfile', '-t', 'inventory-service:dev', '.') }

    # --- Рабочий сервер (deploy/docker-compose.prod.yml, .env.prod) ---
    'prod-build'   { Invoke-Docker ($prod + @('build', 'server')) }
    'prod-migrate' { Invoke-Docker ($prod + @('run', '--rm', 'migrate')) }
    'prod-up'      { Invoke-Docker ($prod + @('up', '-d', '--wait')) }
    'prod-down'    { Invoke-Docker ($prod + @('down')) }
    'prod-ps'      { Invoke-Docker ($prod + @('ps')) }
    'prod-logs'    { Invoke-Docker ($prod + @('logs', '-f', '--tail', '200', 'server')) }
    'prod-agents'  { Invoke-Docker ($prod + @('run', '--rm', 'agentctl', 'list')) }
    # Остаток аргументов уходит в agentctl как есть:
    #   .\make.ps1 prod-ctl token -label "раскатка" -days 14 -uses 600
    'prod-ctl'     { Invoke-Docker ($prod + @('run', '--rm', 'agentctl') + $Rest) }
    'ci' {
        # Одной командой в одном контейнере: подъём контейнера на каждый шаг
        # стоит дороже самих проверок.
        Invoke-Docker ($run + @('sh', '-c', 'gofmt -l . && go vet ./... && golangci-lint run ./... && go test ./... -race -count=1'))
        Write-Host 'CI: всё зелёное' -ForegroundColor Green
    }
    'smoke' {
        $port = if ($env:HTTP_EXPOSED_PORT) { $env:HTTP_EXPOSED_PORT } else { '8090' }
        Write-Host '--- /health ---'
        (Invoke-WebRequest -Uri "http://localhost:$port/health" -UseBasicParsing).Content
        Write-Host '--- /ready ---'
        (Invoke-WebRequest -Uri "http://localhost:$port/ready" -UseBasicParsing).Content
    }
    default {
        Write-Host "Неизвестная цель: $Target. Список — .\make.ps1 help" -ForegroundColor Red
        exit 1
    }
}
