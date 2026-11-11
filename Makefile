MAKEFLAGS       += --no-print-directory

.PHONY: tidy
tidy:					##@ Tidy go.mod and go.sum.
	@go mod tidy

.PHONY: fmt
fmt:					##@ Format Go source files.
	@gofmt -w *.go tests/*.go

.PHONY: lint
lint: tidy				##@ Lint all packages.
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run --timeout 5m --build-tags integration && \
		echo "done."; \
	else \
		echo "golangci-lint is not installed. Please install it from https://github.com/golangci/golangci-lint"; \
		exit 1; \
	fi

.PHONY: build
build:					##@ Compile all packages.
	@go build ./...

.PHONY: test
test: tidy				##@ Test packages without a browser.
	@if command -v gotestsum >/dev/null 2>&1; then \
		gotestsum --format testname --format-icons text -- -race -count 1 -failfast -v ./...; \
	else \
		go test -race -count 1 -failfast -v ./...; \
	fi

.PHONY: test-compose
test-compose:				##@ Run unit and browser integration tests in Docker Compose.
	@set -eu; \
	trap 'status=$$?; trap - EXIT; docker compose -f tests/compose.yaml down --remove-orphans || { cleanup_status=$$?; if [ "$$status" -eq 0 ]; then status=$$cleanup_status; fi; }; exit $$status' EXIT; \
	trap 'exit 130' INT; \
	trap 'exit 143' TERM; \
	docker compose -f tests/compose.yaml up --build --abort-on-container-exit --exit-code-from tests

.PHONY: help
help:					##@ (Default) Show help.
	@printf "\nUsage: make <command>\n"
	@grep -F -h "##@" $(MAKEFILE_LIST) | grep -F -v grep -F | sed -e 's/\\$$//' | awk 'BEGIN {FS = ":*[[:space:]]*##@[[:space:]]*"}; \
	{ \
		if($$2 == "") \
			pass; \
		else if($$0 ~ /^#/) \
			printf "\n%s\n", $$2; \
		else if($$1 == "") \
			printf "     %-20s%s\n", "", $$2; \
		else \
			printf "\n    \033[34m%-20s\033[0m %s\n", $$1, $$2; \
	}'
	@printf "\n"

.DEFAULT_GOAL := help
