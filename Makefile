SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help
.NOTPARALLEL:
VERSION ?= dev
.PHONY: help install lint test build check dev stop clean
help:
	@echo 'make check: complete five-platform archive build, lint and race/consumer checks'
install:
	mise trust .mise.toml
	mise install go python actionlint shellcheck
	mise exec -- go mod download
	cd sdk && mise exec -- go mod download
lint:
	mise exec -- bash scripts/lint.sh
	mise exec -- actionlint
	mise exec -- shellcheck scripts/*.sh .github/scripts/*.sh
test:
	mise exec -- bash scripts/test.sh
build:
	mise exec -- env VERSION="$(VERSION)" bash scripts/build.sh
check: build lint test
dev stop:
	@echo '$@: unsupported: interactive arcade sessions require caller-owned terminal and account configuration'
clean:
	mise exec -- python3 -c 'import shutil; [shutil.rmtree(p, ignore_errors=True) for p in ("dist", ".artifacts")]'
