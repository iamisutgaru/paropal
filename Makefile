SHELL := /bin/bash

.PHONY: build static-tar test

static-tar:
	./scripts/build-sjb-tar.sh

build: static-tar
	go build -o daemon .

test: static-tar
	go test ./...
	go vet ./...
