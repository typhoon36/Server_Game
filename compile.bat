@echo off
chcp 65001 >nul
rem ============================================================
rem  compile.bat : protocol.proto 를 Go 용과 Unity C# 용으로 한 번에 번역하고 검사합니다.
rem  사용법 : 이 파일을 더블클릭하면 됩니다.
rem ============================================================

rem 이 배치 파일이 있는 폴더(GameServer)로 작업 위치를 옮깁니다.
cd /d "%~dp0"

rem [수정 필요] 유니티 프로젝트의 Assets\Scripts\Protocol 폴더 경로를 적으세요.
set "UNITY_OUT=C:\Users\strom\Desktop\Unity\MobileGame\Assets\Scripts\Protocol"

rem protoc 번역기가 설치되어 있는지 확인합니다.
where protoc >nul 2>nul
if errorlevel 1 goto :no_protoc

rem Go 번역 플러그인이 설치되어 있는지 확인합니다.
where protoc-gen-go >nul 2>nul
if errorlevel 1 goto :no_gen_go

rem Go 가 설치되어 있는지 확인합니다.
where go >nul 2>nul
if errorlevel 1 goto :no_go

rem protocol.proto 파일이 이 폴더에 있는지 확인합니다.
if not exist "protocol.proto" goto :no_proto

rem 유니티 출력 폴더가 없으면 새로 만듭니다.
if not exist "%UNITY_OUT%" mkdir "%UNITY_OUT%"

rem ---------- 1) Go 서버용 번역 (protocol.pb.go 생성) ----------
echo [1/3] Generating Go file...
protoc --go_out=. --go_opt=paths=source_relative protocol.proto
if errorlevel 1 goto :fail_go
if not exist "protocol.pb.go" goto :fail_go

rem ---------- 2) 유니티 C# 번역 (Protocol.cs 생성) ----------
echo [2/3] Generating Unity C# file...
protoc --csharp_out="%UNITY_OUT%" protocol.proto
if errorlevel 1 goto :fail_cs
if not exist "%UNITY_OUT%\Protocol.cs" goto :fail_cs

rem ---------- 3) Go 서버 코드 검사 (번역 결과와 main.go 가 서로 맞는지 확인) ----------
echo [3/3] Checking Go server code...
go vet .
if errorlevel 1 goto :fail_vet

echo.
echo [DONE] protocol.pb.go and Protocol.cs were generated and the Go server code is OK.
echo Unity folder: %UNITY_OUT%
goto :end

:no_protoc
echo [ERROR] protoc not found. Add C:\protoc\bin to Path.
goto :end

:no_gen_go
echo [ERROR] protoc-gen-go not found. Run: go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
goto :end

:no_go
echo [ERROR] go not found. Install Go from https://go.dev/dl
goto :end

:no_proto
echo [ERROR] protocol.proto not found in this folder.
goto :end

:fail_go
echo [ERROR] Go compile failed.
goto :end

:fail_cs
echo [ERROR] C# compile failed. Check the UNITY_OUT path.
goto :end

:fail_vet
echo [ERROR] Go code check failed. Replace main.go with the new version first, then run again.
goto :end

rem 창이 바로 닫히지 않게 키 입력을 기다립니다.
:end
echo.
pause