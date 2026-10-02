@echo off
setlocal enableextensions

rem Раскатка агента групповой политикой.
rem
rem Запускается сценарием запуска компьютера (Конфигурация компьютера ->
rem Конфигурация Windows -> Сценарии -> Автозагрузка), то есть от имени SYSTEM
rem и без человека у экрана. Отсюда все отличия от ручного установщика:
rem ничего не спрашивает, ничего не ждёт, при незаданном вопросе выходит с
rem ошибкой в журнал, а не с приглашением ввода.
rem
rem Он же обновляет: сравнивает версию в пакете с установленной и, если они
rem разошлись, подменяет программу. Отдельного механизма обновления нет
rem намеренно — машина, которая почему-то пропустила обновление, догонит его
rem при следующей загрузке, и никакого списка отставших вести не нужно.
rem
rem Имя файла, имена переменных и имена читаемых файлов здесь латиницей —
rem включая имя самого скрипта. В ручном установщике кодовая страница
rem выставляется первой строкой, и кириллица работает; здесь полагаться на это
rem нельзя. Проверено: вызов «раскатка.cmd» из cmd.exe с чужой кодовой
rem страницей заканчивается «не является внутренней или внешней командой», а
rem групповая политика вызывает этот файл именно так — без консоли и без
rem гарантий по кодовой странице.

rem Адрес службы — в файле server.txt рядом, одной строкой. Значение ниже
rem используется, только если файла нет.
rem
rem Отдельным файлом, потому что адрес правится в один приём для всего пакета:
rem держать его в двух скриптах значит однажды поправить один и раскатать
rem половину парка на несуществующий адрес.
set "SERVER=http://inventory:8080"
set "SERVICE=InventoryAgent"

set "SRC=%~dp0"
if exist "%SRC%server.txt" set /p SERVER=<"%SRC%server.txt"
set "DIR=%ProgramFiles%\inventory-agent"
set "DATA=%ProgramData%\inventory-agent"
set "LOG=%DATA%\rollout.log"

if not exist "%DATA%" mkdir "%DATA%" >nul 2>&1

rem --- что лежит в пакете ---
if not exist "%SRC%agent.exe" (
    call :log "ОШИБКА: в пакете нет agent.exe (%SRC%)"
    exit /b 1
)

set "NEW="
if exist "%SRC%version.txt" set /p NEW=<"%SRC%version.txt"
if not defined NEW (
    call :log "ОШИБКА: в пакете нет version.txt — нечем сравнить версии"
    exit /b 1
)

rem --- что стоит на машине ---
rem
rem Версия читается у установленной программы, а не из реестра: реестр может
rem говорить что угодно, а работает именно этот файл.
set "OLD="
if exist "%DIR%\agent.exe" (
    for /f "usebackq delims=" %%v in (`"%DIR%\agent.exe" version 2^>nul`) do set "OLD=%%v"
)

sc query "%SERVICE%" >nul 2>&1
if errorlevel 1 goto install

rem --- служба уже есть ---
rem
rem Совпали версии — выходим молча, не написав ни строки. Скрипт выполняется
rem при каждой загрузке на каждой машине парка: строка «всё в порядке» в
rem журнале превратила бы его в бесполезную ленту за считанные недели.
if "%OLD%"=="%NEW%" exit /b 0

call :log "обновление: было %OLD%, стало %NEW%"

net stop "%SERVICE%" >nul 2>&1
copy /y "%SRC%agent.exe" "%DIR%\agent.exe" >nul
if errorlevel 1 (
    call :log "ОШИБКА: не удалось заменить agent.exe — служба осталась на версии %OLD%"
    net start "%SERVICE%" >nul 2>&1
    exit /b 1
)
net start "%SERVICE%" >nul 2>&1
if errorlevel 1 (
    call :log "ОШИБКА: программа заменена, но служба не запустилась"
    exit /b 1
)
call :log "обновлено до %NEW%"
exit /b 0

:install
rem --- токен раскатки ---
rem
rem Машине, у которой уже есть состояние агента, токен не нужен: ключ у неё с
rem прошлой установки. Требовать токен значило бы, что переустановка службы
rem на любой машине упирается в поход к администратору за новым токеном.
set "TOKEN="
set "ENROLLED="
if exist "%DATA%\state.json" set "ENROLLED=1"
if exist "%SRC%token.txt" set /p TOKEN=<"%SRC%token.txt"

if not defined ENROLLED if not defined TOKEN (
    call :log "ОШИБКА: нет token.txt в пакете и нет отметки агента — регистрировать нечем"
    exit /b 1
)


rem --- доверие к своей подписи ---
rem
rem Если в пакете лежит открытая часть сертификата, она ставится в доверенные
rem до установки самой программы. Иначе подписанный агент ничем не лучше
rem неподписанного: подпись есть, а доверия к ней на машине нет.
rem
rem Два хранилища, а не одно. Root — потому что сертификат самоподписанный и
rem сам себе корень, без этого цепочка не строится. TrustedPublisher — потому
rem что политики вида «запускать только программы доверенных издателей»
rem смотрят именно туда.
if exist "%SRC%подпись.cer" (
    certutil -addstore -f Root "%SRC%подпись.cer" >nul 2>&1
    certutil -addstore -f TrustedPublisher "%SRC%подпись.cer" >nul 2>&1
)

if not exist "%DIR%" mkdir "%DIR%" >nul 2>&1
copy /y "%SRC%agent.exe" "%DIR%\agent.exe" >nul
if errorlevel 1 (
    call :log "ОШИБКА: не удалось скопировать agent.exe в %DIR%"
    exit /b 1
)

if defined TOKEN (
    "%DIR%\agent.exe" install -server "%SERVER%" -token "%TOKEN%" >>"%LOG%" 2>&1
) else (
    "%DIR%\agent.exe" install -server "%SERVER%" >>"%LOG%" 2>&1
)
if errorlevel 1 (
    call :log "ОШИБКА: установка службы не удалась"
    exit /b 1
)

call :log "установлено %NEW%"
exit /b 0

:log
echo %date% %time% %~1>>"%LOG%"
goto :eof
