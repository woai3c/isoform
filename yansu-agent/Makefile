.PHONY: build build-all clean install test

VERSION ?= dev
BINARY_NAME = yansu
BUILD_DIR = build
LDFLAGS = -ldflags="-s -w -X main.version=$(VERSION)"

# Default target
build:
	go build $(LDFLAGS) -o $(BINARY_NAME) ./cmd/yansu/

# Build for all platforms. Darwin targets require a macOS SDK/compiler for
# native Keychain access; they must not fall back to a shared security helper.
build-all: clean
	mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=1 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 ./cmd/yansu/
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 ./cmd/yansu/
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 ./cmd/yansu/
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64 ./cmd/yansu/
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe ./cmd/yansu/

clean:
	rm -f $(BINARY_NAME)
	rm -rf $(BUILD_DIR)

install: build
	sudo mv $(BINARY_NAME) /usr/local/bin/

test:
	go test ./...

# Create checksums
checksums:
	cd $(BUILD_DIR) && sha256sum * > checksums.txt
