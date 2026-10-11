SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help
.NOTPARALLEL:
VERSION ?= dev
export PATH := $(CURDIR)/.artifacts/godot/bin:$(PATH)
.PHONY: help install lint test build artifact-check check dev stop clean
help:
	@echo 'make check: five-platform archives, lint, race/consumer and real Godot terminal checks'
install:
	@command -v "$${TERMCADE_XVFB_BIN:-Xvfb}" >/dev/null || { echo "Install Xvfb and Mesa for Godot framebuffer previews (see README)."; exit 1; }
	mise trust .mise.toml
	mise install go python actionlint shellcheck
	mise exec -- go mod download
	cd sdk && mise exec -- go mod download
	mise exec -- python3 scripts/install-godot.py
lint:
	mise exec -- bash scripts/lint.sh
	mise exec -- actionlint
	mise exec -- shellcheck scripts/*.sh .github/scripts/*.sh
test:
	mise exec -- bash scripts/test.sh
build:
	mise exec -- env VERSION="$(VERSION)" bash scripts/build.sh
artifact-check:
	mise exec -- python3 scripts/test-godot-cli.py
check: build lint test artifact-check
dev stop:
	@echo '$@: unsupported: interactive arcade sessions require caller-owned terminal and account configuration'
clean:
	mise exec -- python3 -c 'import shutil; [shutil.rmtree(p, ignore_errors=True) for p in ("dist", ".artifacts")]'
