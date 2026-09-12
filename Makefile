BINARY := margaret-tools
BUILD_DIR := build
MAIN_PKG := ./cmd/margaret-tools

.PHONY: build clean generate test vet run

generate:
	go tool sqlc generate

build:
	go build -o $(BUILD_DIR)/$(BINARY) $(MAIN_PKG)

run: build
	./$(BUILD_DIR)/$(BINARY) $(ARGS)

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -rf $(BUILD_DIR)
