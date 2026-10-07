BIN_DIR          := $(CURDIR)/bin
GOLANGCI         := $(BIN_DIR)/golangci-lint
GOLANGCI_VERSION := v2.12.2
IMAGE            ?= gostodian
VERSION          ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT           ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)

GO      ?= go
PKGS    := ./...

.DEFAULT_GOAL := help

.PHONY: help
help: ## 사용 가능한 타깃 목록
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

## ---------------------------------------------------------------- 도구
.PHONY: tools
tools: $(GOLANGCI) ## 로컬 도구 설치 (bin/)

$(GOLANGCI):
	@mkdir -p $(BIN_DIR)
	@curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b $(BIN_DIR) $(GOLANGCI_VERSION)

.PHONY: hooks
hooks: ## lefthook 훅 설치
	@lefthook install

## ---------------------------------------------------------------- 품질
.PHONY: fmt
fmt: $(GOLANGCI) ## 포맷 적용 (gofumpt + goimports)
	@$(GOLANGCI) fmt

.PHONY: lint
lint: $(GOLANGCI) ## 전체 린트
	@$(GOLANGCI) run

.PHONY: lint-fix
lint-fix: $(GOLANGCI) ## 자동 수정 가능한 린트 이슈 수정
	@$(GOLANGCI) run --fix

.PHONY: tidy
tidy: ## go.mod 정리 및 검증
	@$(GO) mod tidy
	@$(GO) mod verify

.PHONY: test
test: ## 테스트 (race + shuffle)
	@$(GO) test -race -shuffle=on $(PKGS)

.PHONY: cover
cover: ## 커버리지 리포트 생성
	@$(GO) test -race -covermode=atomic -coverprofile=coverage.out $(PKGS)
	@$(GO) tool cover -html=coverage.out -o coverage.html
	@$(GO) tool cover -func=coverage.out | tail -n 1

## ---------------------------------------------------------------- 보안
.PHONY: vuln
vuln: ## govulncheck — 실제 호출 경로 기준 취약점
	@$(GO) run golang.org/x/vuln/cmd/govulncheck@latest $(PKGS)

.PHONY: sec
sec: ## gosec — Go SAST
	@$(GO) run github.com/securego/gosec/v2/cmd/gosec@latest -severity medium -confidence low $(PKGS)

.PHONY: secrets
secrets: ## gitleaks — 히스토리 전체 시크릿 스캔
	@docker run --rm -v "$(CURDIR):/repo" ghcr.io/gitleaks/gitleaks:latest git /repo --redact --no-banner

.PHONY: scan-config
scan-config: ## trivy — Dockerfile / compose 오설정 스캔
	@docker run --rm -v "$(CURDIR):/w" -w /w aquasec/trivy:latest config --severity HIGH,CRITICAL .

.PHONY: scan-image
scan-image: docker ## trivy — 빌드된 이미지 취약점 스캔
	@docker run --rm -v /var/run/docker.sock:/var/run/docker.sock aquasec/trivy:latest \
	image --severity HIGH,CRITICAL --ignore-unfixed $(IMAGE):$(VERSION)

.PHONY: audit
audit: lint vuln sec secrets scan-config ## 보안 전체 점검 (CI와 동일)

## ---------------------------------------------------------------- 빌드
.PHONY: build
build: ## 점검 머신용 gostodian, 홈랩용 gostodian-agent
	@CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)" -o $(BIN_DIR)/gostodian ./cmd/gostodian
	@CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o $(BIN_DIR)/gostodian-agent ./cmd/gostodian-agent

.PHONY: docker
docker: ## 컨테이너 이미지 빌드
	@docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t $(IMAGE):$(VERSION) -t $(IMAGE):latest .

.PHONY: up
up: ## compose 기동
	@docker compose up -d

.PHONY: down
down: ## compose 종료
	@docker compose down

.PHONY: clean
clean: ## 산출물 정리
	@rm -rf $(BIN_DIR) coverage.out coverage.html *.sarif
