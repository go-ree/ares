SHELL := /bin/sh
.DEFAULT_GOAL := help

APP_NAME ?= ares
VERSION ?= dev
IMAGE_REPOSITORY ?= ghcr.io/go-ree/ares
IMAGE_TAG ?= $(VERSION)
IMAGE := $(IMAGE_REPOSITORY):$(IMAGE_TAG)
BUILD_DIR ?= build/$(VERSION)

GO ?= go
NPM ?= npm
DOCKER ?= docker
SYFT ?= syft
TRIVY ?= trivy

SWAG_VERSION ?= v1.16.4
GOVULNCHECK_VERSION ?= v1.7.0
ACTIONLINT_VERSION ?= v1.7.12
GO_VERSION ?= 1.26.8
NODE_VERSION ?= 24.20.0
NPM_VERSION ?= 11.19.1
SYFT_VERSION ?= v1.51.1
TRIVY_VERSION ?= v0.74.0
GO_PACKAGES ?= . ./internal/...
RACE_PACKAGES ?= ./internal/cli ./internal/config ./internal/db ./internal/workflow ./internal/executor/... ./internal/integration ./internal/jenkins ./internal/k8s ./internal/publish ./internal/environment ./internal/api/...

.PHONY: help all clean fmt-check mod-check test db-integration db-account-integration vet race vuln toolchain-check workflow-check backend-check frontend-install frontend-check frontend-audit swagger swagger-check compose-config build build-linux-amd64 build-linux-arm64 build-darwin-amd64 build-darwin-arm64 build-windows-amd64 docker docker-build syft-version-check trivy-version-check sbom image-scan verify

help: ## 显示可用命令
	@awk 'BEGIN {FS = ":.*## "; printf "Ares 开发命令\n\n"} /^[a-zA-Z0-9_.-]+:.*## / {printf "  %-24s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

all: verify build ## 执行完整检查并构建当前平台二进制

clean: ## 删除仓库 build 目录下的构建产物
	@repo_root="$$(git rev-parse --show-toplevel)"; \
	current_dir="$$(pwd -P)"; \
	if [ "$$current_dir" != "$$repo_root" ]; then \
		echo "请在仓库根目录执行 make clean"; \
		exit 1; \
	fi; \
	rm -rf -- "$$repo_root/build"

fmt-check: ## 检查 Go 源码格式
	@set -eu; \
	unformatted_files="$$(git ls-files -z '*.go' | xargs -0 gofmt -l --)"; \
	if [ -n "$$unformatted_files" ]; then \
		echo "以下 Go 文件需要执行 gofmt："; \
		echo "$$unformatted_files"; \
		exit 1; \
	fi

mod-check: ## 校验 Go 模块内容及 go.mod/go.sum 一致性
	$(GO) mod verify
	$(GO) mod tidy -diff

test: ## 运行 Go 全量测试
	$(GO) test -count=1 $(GO_PACKAGES)

db-integration: ## 在 MySQL 8.4 上运行数据库迁移集成测试（需要 ARES_TEST_MYSQL_DSN）
	@test -n "$$ARES_TEST_MYSQL_DSN" || { echo "请设置 MySQL 8.4 管理员 DSN：ARES_TEST_MYSQL_DSN"; exit 1; }
	$(GO) test -count=1 -run '^(TestPreW04FixtureIsImmutable|TestMySQL84Migrations)$$' ./internal/db
	$(GO) test -count=1 -run '^(TestMigrationCLIExitCodesAndSafeOutput|TestServeRejectsEmptySchemaBeforeStartingRuntime)$$' .

db-account-integration: ## 在 MySQL 8.4 容器中动态验证最小权限账号初始化
	@test -n "$$ARES_TEST_MYSQL_CONTAINER" || { echo "请设置 MySQL 8.4 容器名：ARES_TEST_MYSQL_CONTAINER"; exit 1; }
	@test -n "$$ARES_TEST_MYSQL_ROOT_PASSWORD" || { echo "请设置容器 root 密码：ARES_TEST_MYSQL_ROOT_PASSWORD"; exit 1; }
	DOCKER_BIN="$(DOCKER)" bash deploy/compose/mysql/account-init-integration.sh

vet: ## 运行 Go 静态检查
	$(GO) vet $(GO_PACKAGES)

race: ## 对并发和运行时关键包执行 Race Detector
	$(GO) test -race -count=1 $(RACE_PACKAGES)

vuln: ## 扫描 Go 代码中的可达漏洞
	$(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) $(GO_PACKAGES)

toolchain-check: ## 检查仓库各处固定工具版本保持一致
	@set -eu; \
	check_contains() { \
		file="$$1"; expected="$$2"; \
		if ! grep -Fq -- "$$expected" "$$file"; then \
			echo "版本不一致：$$file 缺少 $$expected"; \
			exit 1; \
		fi; \
	}; \
	check_contains Makefile 'GO_VERSION ?= $(GO_VERSION)'; \
	check_contains go.mod 'toolchain go$(GO_VERSION)'; \
	check_contains Dockerfile 'FROM golang:$(GO_VERSION)-'; \
	check_contains .github/workflows/quality-gates.yml 'GO_VERSION: "$(GO_VERSION)"'; \
	check_contains CONTRIBUTING.md 'Go `$(GO_VERSION)`'; \
	check_contains Makefile 'NODE_VERSION ?= $(NODE_VERSION)'; \
	check_contains frontend/Dockerfile 'FROM node:$(NODE_VERSION)-'; \
	check_contains frontend/package.json '"node": "$(NODE_VERSION)"'; \
	check_contains .github/workflows/quality-gates.yml 'NODE_VERSION: "$(NODE_VERSION)"'; \
	check_contains CONTRIBUTING.md 'Node.js `$(NODE_VERSION)`'; \
	check_contains Makefile 'NPM_VERSION ?= $(NPM_VERSION)'; \
	check_contains frontend/Dockerfile 'npm@$(NPM_VERSION)'; \
	check_contains frontend/package.json '"packageManager": "npm@$(NPM_VERSION)"'; \
	check_contains frontend/package.json '"npm": "$(NPM_VERSION)"'; \
	check_contains .github/workflows/quality-gates.yml 'NPM_VERSION: "$(NPM_VERSION)"'; \
	check_contains CONTRIBUTING.md 'npm `$(NPM_VERSION)`'; \
	check_contains Makefile 'SWAG_VERSION ?= $(SWAG_VERSION)'; \
	check_contains docs/development/quality-gates.md 'Swag `$(SWAG_VERSION)`'; \
	check_contains Makefile 'GOVULNCHECK_VERSION ?= $(GOVULNCHECK_VERSION)'; \
	check_contains .github/workflows/quality-gates.yml 'GOVULNCHECK_VERSION: $(GOVULNCHECK_VERSION)'; \
	check_contains docs/development/quality-gates.md 'govulncheck `$(GOVULNCHECK_VERSION)`'; \
	check_contains Makefile 'ACTIONLINT_VERSION ?= $(ACTIONLINT_VERSION)'; \
	check_contains .github/workflows/quality-gates.yml 'ACTIONLINT_VERSION: $(ACTIONLINT_VERSION)'; \
	check_contains docs/development/quality-gates.md 'actionlint `$(ACTIONLINT_VERSION)`'; \
	check_contains Makefile 'SYFT_VERSION ?= $(SYFT_VERSION)'; \
	check_contains .github/workflows/container-security.yml 'SYFT_VERSION: $(SYFT_VERSION)'; \
	check_contains docs/development/quality-gates.md 'Syft `$(SYFT_VERSION)`'; \
	check_contains Makefile 'TRIVY_VERSION ?= $(TRIVY_VERSION)'; \
	check_contains .github/workflows/container-security.yml 'TRIVY_VERSION: $(TRIVY_VERSION)'; \
	check_contains docs/development/quality-gates.md 'Trivy `$(TRIVY_VERSION)`'

workflow-check: toolchain-check ## 检查固定工具版本与 GitHub Actions 工作流
	$(GO) run github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION)

backend-check: fmt-check mod-check test vet race vuln swagger-check ## 执行后端质量门禁

frontend-install: ## 按 lockfile 安装前端依赖
	$(NPM) --prefix frontend ci

frontend-check: ## 执行前端格式、Lint、类型与构建检查
	$(NPM) --prefix frontend run eslint:check
	$(NPM) --prefix frontend run prettier:check
	$(NPM) --prefix frontend run type-check
	$(NPM) --prefix frontend run build

frontend-audit: ## 扫描全部前端依赖的 high/critical 漏洞
	$(NPM) --prefix frontend audit --audit-level=high

swagger: ## 重新生成 Swagger 文件
	$(GO) run github.com/swaggo/swag/cmd/swag@$(SWAG_VERSION) init -g main.go -o internal/swagger

swagger-check: swagger ## 校验 Swagger 已提交且可重复生成
	git diff --exit-code -- internal/swagger
	@untracked_files="$$(git ls-files --others --exclude-standard -- internal/swagger)"; \
	if [ -n "$$untracked_files" ]; then \
		echo "以下 Swagger 生成文件尚未纳入版本控制："; \
		echo "$$untracked_files"; \
		exit 1; \
	fi

compose-config: ## 校验 Docker Compose 配置
	bash -n deploy/compose/mysql/01-create-users.sh
	bash -n deploy/compose/mysql/account-init-integration.sh
	$(DOCKER) compose config --quiet

build: ## 构建当前平台二进制
	mkdir -p "$(BUILD_DIR)"
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags='-s -w' -o "$(BUILD_DIR)/$(APP_NAME)" ./main.go

build-linux-amd64: ## 构建 Linux amd64 二进制
	mkdir -p "$(BUILD_DIR)/linux-amd64"
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build -trimpath -ldflags='-s -w' -o "$(BUILD_DIR)/linux-amd64/$(APP_NAME)" ./main.go

build-linux-arm64: ## 构建 Linux arm64 二进制
	mkdir -p "$(BUILD_DIR)/linux-arm64"
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -trimpath -ldflags='-s -w' -o "$(BUILD_DIR)/linux-arm64/$(APP_NAME)" ./main.go

build-darwin-amd64: ## 构建 macOS amd64 二进制
	mkdir -p "$(BUILD_DIR)/darwin-amd64"
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 $(GO) build -trimpath -ldflags='-s -w' -o "$(BUILD_DIR)/darwin-amd64/$(APP_NAME)" ./main.go

build-darwin-arm64: ## 构建 macOS arm64 二进制
	mkdir -p "$(BUILD_DIR)/darwin-arm64"
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 $(GO) build -trimpath -ldflags='-s -w' -o "$(BUILD_DIR)/darwin-arm64/$(APP_NAME)" ./main.go

build-windows-amd64: ## 构建 Windows amd64 二进制
	mkdir -p "$(BUILD_DIR)/windows-amd64"
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 $(GO) build -trimpath -ldflags='-s -w' -o "$(BUILD_DIR)/windows-amd64/$(APP_NAME).exe" ./main.go

docker: docker-build ## docker-build 的兼容别名

docker-build: ## 构建 Ares 后端容器镜像
	$(DOCKER) build --pull --tag "$(IMAGE)" .

syft-version-check: ## 校验本地 Syft 版本
	@command -v "$(SYFT)" >/dev/null 2>&1 || { echo "未安装 $(SYFT)"; exit 1; }
	@actual_version="$$("$(SYFT)" version 2>/dev/null | awk '$$1 == "Version:" { print $$2; exit }')"; \
	expected_version="$(patsubst v%,%,$(SYFT_VERSION))"; \
	if [ "$$actual_version" != "$$expected_version" ]; then \
		echo "Syft 版本不一致：期望 $$expected_version，实际 $${actual_version:-未知}"; \
		exit 1; \
	fi

trivy-version-check: ## 校验本地 Trivy 版本
	@command -v "$(TRIVY)" >/dev/null 2>&1 || { echo "未安装 $(TRIVY)"; exit 1; }
	@actual_version="$$("$(TRIVY)" --version 2>/dev/null | awk '$$1 == "Version:" { print $$2; exit }')"; \
	expected_version="$(patsubst v%,%,$(TRIVY_VERSION))"; \
	if [ "$$actual_version" != "$$expected_version" ]; then \
		echo "Trivy 版本不一致：期望 $$expected_version，实际 $${actual_version:-未知}"; \
		exit 1; \
	fi

sbom: syft-version-check ## 为已构建镜像生成 CycloneDX SBOM（需要 syft）
	mkdir -p "$(BUILD_DIR)"
	$(SYFT) "$(IMAGE)" -o "cyclonedx-json=$(BUILD_DIR)/sbom.cdx.json"

image-scan: trivy-version-check ## 扫描镜像中的 high/critical 漏洞（需要 trivy）
	$(TRIVY) image --exit-code 1 --severity HIGH,CRITICAL "$(IMAGE)"

verify: workflow-check backend-check frontend-check frontend-audit compose-config ## 执行合并前完整质量门禁
