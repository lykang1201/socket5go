@echo off
echo ===== 开始编译 Linux 版本 =====

if not exist build mkdir build
if not exist build\linux_amd64 mkdir build\linux_amd64
if not exist build\linux_arm64 mkdir build\linux_arm64

echo.
echo --- 编译 main_server (amd64) ---
set GOOS=linux
set GOARCH=amd64
set CGO_ENABLED=0
go build -ldflags "-s -w" -o ./build/linux_amd64/main_server ./cmd/server/main_server.go

echo.
echo --- 编译 main_server (arm64) ---
set GOOS=linux
set GOARCH=arm64
set CGO_ENABLED=0
go build -ldflags "-s -w" -o ./build/linux_arm64/main_server ./cmd/server/main_server.go

echo.
echo --- 编译 main_client (amd64) ---
set GOOS=linux
set GOARCH=amd64
set CGO_ENABLED=0
go build -ldflags "-s -w" -o ./build/linux_amd64/main_client ./cmd/client/main_client.go

echo.
echo --- 编译 main_client (arm64) ---
set GOOS=linux
set GOARCH=arm64
set CGO_ENABLED=0
go build -ldflags "-s -w" -o ./build/linux_arm64/main_client ./cmd/client/main_client.go

echo.
echo ===== 编译完成 =====
echo 输出目录: ./build/linux_amd64/
echo 输出目录: ./build/linux_arm64/
