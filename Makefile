PLUGIN_ID := com.company.external-push-bridge
VERSION := 0.1.0
DIST_DIR := dist
SERVER_BIN_LINUX_AMD64 := server/dist/plugin-linux-amd64
SERVER_BIN_LINUX_ARM64 := server/dist/plugin-linux-arm64
BUNDLE := $(DIST_DIR)/$(PLUGIN_ID)-$(VERSION).tar.gz

.PHONY: all fmt test build bundle clean

all: test build

fmt:
	gofmt -w ./server

test:
	go test ./...

build:
	mkdir -p server/dist
	GOOS=linux GOARCH=amd64 go build -o $(SERVER_BIN_LINUX_AMD64) ./server
	GOOS=linux GOARCH=arm64 go build -o $(SERVER_BIN_LINUX_ARM64) ./server

bundle: build
	mkdir -p $(DIST_DIR)
	tar -czf $(BUNDLE) plugin.json server/dist

clean:
	rm -rf server/dist dist
