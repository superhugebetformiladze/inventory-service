@echo off
chcp 65001 >nul
title Установка агента инвентаризации

rem Установка агента на рабочую станцию в два действия: двойной щелчок по
rem этому файлу и подтверждение запроса Windows.
rem
rem Перед раскаткой по парку заполнить СЕРВЕР ниже, а токен раскатки положить
rem в файл token.txt рядом. Токен намеренно не вписан сюда: этот файл ездит по
rem сотням машин, а токен даёт право зарегистрировать любую из них.

rem Адрес службы — в файле server.txt рядом, одной строкой. Значение ниже
rem используется, только если файла нет.
set "SERVER=http://localhost:8090"
set "SERVICE=InventoryAgent"

if exist "%~dp0server.txt" set /p SERVER=<"%~dp0server.txt"

rem --- права администратора ---
net session >nul 2>&1
if not errorlevel 1 goto elevated

echo Для установки нужны права администратора.
echo Подтвердите запрос Windows, который сейчас появится.
powershell -NoProfile -Command "Start-Process -FilePath '%~f0' -Verb RunAs"
exit /b

:elevated
cd /d "%~dp0"

if not exist "%~dp0agent.exe" (
    echo ОШИБКА: рядом с этим файлом нет agent.exe
    goto end
)

set "DIR=%ProgramFiles%\inventory-agent"

rem --- служба уже стоит: это обновление, токен не нужен ---
sc query "%SERVICE%" >nul 2>&1
if errorlevel 1 goto install

echo Служба уже установлена — обновляю программу.
net stop "%SERVICE%" >nul 2>&1
copy /y "%~dp0agent.exe" "%DIR%\agent.exe" >nul
if errorlevel 1 (
    echo ОШИБКА: не удалось заменить agent.exe
    goto end
)
net start "%SERVICE%" >nul 2>&1
if errorlevel 1 (
    echo ОШИБКА: программа заменена, но служба не запустилась
    goto end
)
echo Готово: служба работает на новой версии.
goto show

:install
rem --- токен раскатки: из файла рядом или спросить ---
rem
rem Машина, где уже есть состояние агента, в токене не нуждается: ключ у неё
rem с прошлой установки. Требовать токен значило бы не дать переустановить
rem агента без похода к администратору за новым.
set "TOKEN="
set "ENROLLED="
if exist "%ProgramData%\inventory-agent\state.json" set "ENROLLED=1"
if exist "%~dp0token.txt" set /p TOKEN=<"%~dp0token.txt"
if defined ENROLLED goto haveToken
if "%TOKEN%"=="" set /p "TOKEN=Токен раскатки (enr_...): "
if "%TOKEN%"=="" (
    echo ОШИБКА: без токена раскатки машину не зарегистрировать.
    echo Получить его: make token LABEL="раскатка" DAYS=14 USES=600
    goto end
)
:haveToken

echo Копирую программу в "%DIR%"

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
if exist "%~dp0подпись.cer" (
    certutil -addstore -f Root "%~dp0подпись.cer" >nul 2>&1
    certutil -addstore -f TrustedPublisher "%~dp0подпись.cer" >nul 2>&1
)

if not exist "%DIR%" mkdir "%DIR%"
copy /y "%~dp0agent.exe" "%DIR%\agent.exe" >nul
if errorlevel 1 (
    echo ОШИБКА: не удалось скопировать agent.exe
    goto end
)

if defined TOKEN (
    "%DIR%\agent.exe" install -server "%SERVER%" -token "%TOKEN%"
) else (
    echo Машина уже зарегистрирована — ставлю службу с прежним ключом.
    "%DIR%\agent.exe" install -server "%SERVER%"
)
if errorlevel 1 goto end

:show
echo.
"%DIR%\agent.exe" status

:end
echo.
pause
