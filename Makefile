.DEFAULT_GOAL := all

SHELL := /usr/bin/env bash

.PHONY: all
all: compile

.PHONY: build
build:
	rm -rf build && mkdir build
	for RAW_DIST in $$(go tool dist list); do \
		DIST=($${RAW_DIST//// }) && export GOOS=$${DIST[0]} && export GOARCH=$${DIST[1]} && CURRENT="build/go-conversion-gen-$$GOOS-$$GOARCH" \
			&& go build -o "$$CURRENT" github.com/wk8/go-conversion-gen/cmd \
			|| rm -vf "$$CURRENT"; \
	done
