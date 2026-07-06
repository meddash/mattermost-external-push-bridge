PLUGIN_ID := com.company.external-push-bridge
VERSION := 0.1.0
DIST_DIR := dist
STAGING_DIR := $(DIST_DIR)/bundle
GO_BUILD_LDFLAGS := -s -w
SERVER_BIN_LINUX_AMD64 := server/dist/plugin-linux-amd64
SERVER_BIN_LINUX_ARM64 := server/dist/plugin-linux-arm64
BUNDLE := $(DIST_DIR)/$(PLUGIN_ID)-$(VERSION).tar.gz

.PHONY: all fmt test build bundle validate-bundle clean

all: test build

fmt:
	gofmt -w ./server

test:
	go test ./...

build:
	mkdir -p server/dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(GO_BUILD_LDFLAGS)" -o $(SERVER_BIN_LINUX_AMD64) ./server
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "$(GO_BUILD_LDFLAGS)" -o $(SERVER_BIN_LINUX_ARM64) ./server

bundle: build
	mkdir -p $(DIST_DIR)
	rm -rf $(STAGING_DIR)
	mkdir -p $(STAGING_DIR)/server/dist
	cp plugin.json $(STAGING_DIR)/plugin.json
	cp $(SERVER_BIN_LINUX_AMD64) $(STAGING_DIR)/$(SERVER_BIN_LINUX_AMD64)
	cp $(SERVER_BIN_LINUX_ARM64) $(STAGING_DIR)/$(SERVER_BIN_LINUX_ARM64)
	chmod 755 $(STAGING_DIR)/$(SERVER_BIN_LINUX_AMD64) $(STAGING_DIR)/$(SERVER_BIN_LINUX_ARM64)
	tar -C $(STAGING_DIR) --owner=0 --group=0 --numeric-owner -czf $(BUNDLE) plugin.json server
	rm -rf $(STAGING_DIR)
	$(MAKE) validate-bundle

validate-bundle:
	gzip -t $(BUNDLE)
	tar -tzf $(BUNDLE) | grep -Fx plugin.json >/dev/null
	tar -tzf $(BUNDLE) | grep -Fx server/ >/dev/null
	tar -tzf $(BUNDLE) | grep -Fx server/dist/ >/dev/null
	tar -tzf $(BUNDLE) | grep -Fx server/dist/plugin-linux-amd64 >/dev/null
	tar -tzf $(BUNDLE) | grep -Fx server/dist/plugin-linux-arm64 >/dev/null

clean:
	rm -rf server/dist dist
