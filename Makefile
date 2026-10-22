# MyLock —— 构建脚本
#
#   make                编译全部平台的安装包
#   make build          只编译当前平台，开发时用这个
#   make PLATFORMS=windows/amd64,linux/amd64   只编译指定平台
#   make check          提交前跑一遍 vet 和测试
#   make help           列出所有目标
#
# 跨平台编译不需要 cgo，也不需要在目标系统上操作，任何机器都能编出全部平台。
# 只有 macOS 的签名和 .dmg 需要 macOS 本身，在别的系统上 mygo 会跳过它们。

# go.mod 里 pin 了 mygo 的版本，用 go tool 调用它，免得 PATH 上装的是别的版本。
MYGO ?= go tool mygo

# 产物目录，与 mygo.json 里的 out 保持一致。
OUT ?= build

# make 默认编译的全部平台。darwin/universal 会同时编出 arm64 和 amd64 再合成
# 一个 .app，所以不另外单列 darwin/arm64 和 darwin/amd64。
PLATFORMS ?= linux/amd64 linux/arm64 windows/amd64 windows/arm64 darwin/universal

empty :=
space := $(empty) $(empty)
comma := ,
# mygo build 要的是逗号分隔的列表。
PLATFORM_LIST := $(subst $(space),$(comma),$(strip $(PLATFORMS)))

# 当前机器，决定 make build 编哪个平台。
HOST := $(shell go env GOOS)/$(shell go env GOARCH)

.DEFAULT_GOAL := all

.PHONY: all build web run dev test vet fmt tidy doctor check clean help

## all: 编译全部平台（默认目标）
all:
	$(MYGO) build -platform $(PLATFORM_LIST) -o $(OUT)

## build: 只编译当前平台，开发时用这个
build:
	$(MYGO) build -platform $(HOST) -o $(OUT)

## web: 编译浏览器版本，输出 WebAssembly 静态站点
web:
	$(MYGO) build -platform web -o $(OUT)

## run: 直接运行，不打包
run:
	go run .

## dev: 开发模式，带热重载
dev:
	$(MYGO) dev

## test: 跑测试
test:
	CGO_ENABLED=0 go test ./...

## vet: go vet 加上 mygo 的 UI 生命周期检查（界面代码在 internal/app）
vet:
	$(MYGO) vet ./internal/app

## fmt: 格式化
fmt:
	gofmt -l -w .

## tidy: 整理依赖
tidy:
	go mod tidy

## doctor: 检查开发环境是否齐全
doctor:
	$(MYGO) doctor

## check: vet 加测试，提交前跑一遍
check: vet test

## clean: 删除产物目录
clean:
	rm -rf $(OUT)

## help: 列出所有目标
help:
	@echo "用法：make [目标] [变量=值]"
	@echo
	@grep -hE '^## ' $(MAKEFILE_LIST) | sed 's/^## //' | column -t -s ':'
	@echo
	@echo "变量："
	@echo "  PLATFORMS  要编译的平台，空格分隔（当前：$(PLATFORMS)）"
	@echo "             当前机器是 $(HOST)，make build 编的就是它"
	@echo "  OUT        产物目录（当前：$(OUT)）"
	@echo "  MYGO       mygo 命令（当前：$(MYGO)）"
