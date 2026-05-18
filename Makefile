VERSION ?= $(shell git describe --tags --always --long --dirty 2>/dev/null || echo "dev")
LDFLAGS := -X main.version=$(VERSION)

ifeq ($(OS),Windows_NT)
	BIN := dist/midibridge-client.exe
else
	BIN := dist/midibridge-client
endif

build:
	CGO_ENABLED=1 go build -ldflags "$(LDFLAGS)" -o $(BIN) ./src/

version:
	@echo $(VERSION)

clean:
	rm -rf dist/
