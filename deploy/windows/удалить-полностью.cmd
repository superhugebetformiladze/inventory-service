@echo off
chcp 65001 >nul
title Полное удаление агента инвентаризации

rem Снимает службу, удаляет программу И каталог данных.
rem
rem Вместе с данными удаляется отметка агента. Машина, на которую агента
rem поставят снова, будет для учётной системы НОВОЙ: заведётся вторая
rem карточка, а старую придётся объединять руками. Делать это стоит только
rem при выводе машины из эксплуатации.

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

echo ВНИМАНИЕ: вместе с данными удалится отметка агента.
echo При следующей установке машина попадёт в учёт как новая.
echo.
set "ANSWER="
set /p "ANSWER=Удалить всё, включая отметку? (да/нет): "
if /i not "%ANSWER%"=="да" (
    echo Отменено.
    goto end
)

sc query "%SERVICE%" >nul 2>&1
if errorlevel 1 (
    echo Служба не установлена.
) else (
    if exist "%DIR%\agent.exe" (
        "%DIR%\agent.exe" uninstall
    ) else (
        net stop "%SERVICE%" >nul 2>&1
        sc delete "%SERVICE%" >nul
    )
)

if exist "%DIR%"  rd /s /q "%DIR%"  2>nul
if exist "%DATA%" rd /s /q "%DATA%" 2>nul

if exist "%DIR%"  echo Не удалось удалить "%DIR%"
if exist "%DATA%" echo Не удалось удалить "%DATA%"
if not exist "%DIR%" if not exist "%DATA%" echo Удалено полностью.

:end
echo.
pause
