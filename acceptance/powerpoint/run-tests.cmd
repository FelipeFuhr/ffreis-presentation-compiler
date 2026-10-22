@echo off
rem run-tests.cmd -- the guest-side entry point, started by the scheduled task.
rem
rem It exists because of one Windows constraint: a command sent over SSH runs in
rem a non-interactive session, and PowerPoint COM automation there fails in ways
rem that look exactly like file corruption. A scheduled task registered with /IT
rem runs in the logged-on interactive session instead, so this script is what the
rem host triggers with `schtasks /Run`.
rem
rem It takes no arguments -- schtasks /Run cannot pass any -- and reads run.env,
rem which the host stages next to it.
rem
rem artifacts\exitcode is written LAST and is the host's completion signal, so
rem nothing above may create it early.

setlocal enabledelayedexpansion
set "HERE=%~dp0"
cd /d "%HERE%"

if exist "%HERE%artifacts" rmdir /s /q "%HERE%artifacts"
mkdir "%HERE%artifacts"

set "LOG=%HERE%artifacts\pytest.log"

if not exist "%HERE%run.env" (
  echo run.env not found next to this script -- the host did not stage it > "%LOG%"
  > "%HERE%artifacts\exitcode" echo 3
  exit /b 3
)

for /f "usebackq eol=# tokens=1,* delims==" %%A in ("%HERE%run.env") do set "%%A=%%B"

where python >nul 2>&1
if errorlevel 1 (
  echo python is not on PATH in the interactive session -- see README.md provisioning > "%LOG%"
  > "%HERE%artifacts\exitcode" echo 3
  exit /b 3
)

python -c "import pytest, win32com.client, win32ui, psutil" >> "%LOG%" 2>&1
if errorlevel 1 (
  echo guest dependencies missing; run: python -m pip install -r requirements.txt >> "%LOG%"
  > "%HERE%artifacts\exitcode" echo 3
  exit /b 3
)

python -m pytest -q -p no:cacheprovider ^
  --junitxml="%HERE%artifacts\junit.xml" ^
  >> "%LOG%" 2>&1
set "RC=%ERRORLEVEL%"

python -c "import sys,platform;print('guest python', sys.version);print('windows', platform.platform())" >> "%LOG%" 2>&1

> "%HERE%artifacts\exitcode" echo %RC%
exit /b %RC%
