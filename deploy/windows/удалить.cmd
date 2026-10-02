@echo off
chcp 65001 >nul
title Удаление агента инвентаризации

rem Снимает службу и удаляет программу. Каталог данных ОСТАЁТСЯ.
rem
rem В нём лежит отметка агента — то, по чему учётная система узнаёт эту машину.
rem Поставите агента заново — машина останется той же карточкой. Если нужно
rem стереть и её, есть удалить-полностью.cmd, но это заводит машине вторую
rem карточку при следующей установке.

set "SERVICE=InventoryAgent"
set "DIR=%ProgramFiles%\inventory-agent"
set "DATA=%ProgramData%\inventory-agent"

net session >nul 2>&1
if not errorlevel 1 goto elevated

echo Для удаления нужны права администратора.
echo Подтвердите запрос Windows, который сейчас появится.
powershell -NoProfile -Command "Start-Process -FilePath '%~f0' -Verb RunAs"
exit /b

:elevated
cd /d "%~dp0"

sc query "%SERVICE%" >nul 2>&1
if errorlevel 1 (
    echo Служба не установлена.
) else (
    if exist "%DIR%\agent.exe" (
        "%DIR%\agent.exe" uninstall
    ) else (
        net stop "%SERVICE%" >nul 2>&1
        sc delete "%SERVICE%" >nul
        echo Служба %SERVICE% снята.
    )
)

if exist "%DIR%" (
    rd /s /q "%DIR%" 2>nul
    if exist "%DIR%" (
        echo Программу удалить не удалось, файл занят: "%DIR%"
    ) else (
        echo Программа удалена: "%DIR%"
    )
)

echo.
echo Каталог данных оставлен: "%DATA%"
echo В нём отметка агента — поставите заново, машина останется той же.
echo.
pause
