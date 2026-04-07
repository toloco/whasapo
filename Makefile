VERSION := 0.5.0
LDFLAGS := -s -w -X main.version=$(VERSION)
BINARY  := whasapo

.PHONY: build release release-all clean

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/whasapo

# macOS universal binary only
release: clean
	@mkdir -p dist
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-arm64 ./cmd/whasapo
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-amd64 ./cmd/whasapo
	lipo -create -output dist/$(BINARY) dist/$(BINARY)-arm64 dist/$(BINARY)-amd64
	rm -f dist/$(BINARY)-arm64 dist/$(BINARY)-amd64
	cd dist && zip $(BINARY)-$(VERSION)-macos.zip $(BINARY)
	rm -f dist/$(BINARY)
	@echo "Built: dist/$(BINARY)-$(VERSION)-macos.zip"

# All platforms
release-all: release
	# Linux amd64
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY) ./cmd/whasapo
	cd dist && tar czf $(BINARY)-$(VERSION)-linux-amd64.tar.gz $(BINARY) && rm $(BINARY)
	# Linux arm64
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY) ./cmd/whasapo
	cd dist && tar czf $(BINARY)-$(VERSION)-linux-arm64.tar.gz $(BINARY) && rm $(BINARY)
	# Windows amd64
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY).exe ./cmd/whasapo
	cd dist && zip $(BINARY)-$(VERSION)-windows-amd64.zip $(BINARY).exe && rm $(BINARY).exe
	@echo ""
	@ls -lh dist/$(BINARY)-$(VERSION)-*

clean:
	rm -rf bin dist
