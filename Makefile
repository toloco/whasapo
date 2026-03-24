VERSION := 0.2.0
LDFLAGS := -s -w -X main.version=$(VERSION)
BINARY  := whasapo

.PHONY: build release clean

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/whasapo

release: clean
	@mkdir -p dist
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-arm64 ./cmd/whasapo
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-amd64 ./cmd/whasapo
	lipo -create -output dist/$(BINARY) dist/$(BINARY)-arm64 dist/$(BINARY)-amd64
	rm -f dist/$(BINARY)-arm64 dist/$(BINARY)-amd64
	cd dist && zip $(BINARY)-$(VERSION)-macos.zip $(BINARY)
	@echo ""
	@echo "Built: dist/$(BINARY)-$(VERSION)-macos.zip"
	@ls -lh dist/$(BINARY)-$(VERSION)-macos.zip

clean:
	rm -rf bin dist
