VERSION ?= $(shell git describe --tags --always --long --dirty 2>/dev/null || echo "dev")

ifeq ($(OS),Windows_NT)
	BIN := ../dist/midibridge-client.exe
# Windows 的 mingw 运行库不是系统组件，动态链接会让产物在干净机器上直接报缺少 DLL，因此整包静态链接
# The mingw runtime is not an OS component on Windows, so a dynamic link makes the binary fail with a missing DLL on a clean machine and it must be linked in as a whole
	EXTLDFLAGS := -extldflags '-static'
else
	BIN := ../dist/midibridge-client
	UNAME_S := $(shell uname -s 2>/dev/null)
ifneq ($(UNAME_S),Darwin)
# Linux 只静态链接 libgcc 与 libstdc++，libc 与 libasound 保持动态，这是链接系统音频栈的前提
# Linux links only libgcc and libstdc++ statically and keeps libc and libasound dynamic, which is what linking against the system audio stack requires
	EXTLDFLAGS := -extldflags '-static-libgcc -static-libstdc++'
endif
endif

LDFLAGS := $(strip -X main.version=$(VERSION) $(EXTLDFLAGS))

build:
	CGO_ENABLED=1 go build -ldflags "$(LDFLAGS)" -o $(BIN) .

version:
	@echo $(VERSION)

clean:
	rm -rf ../dist/
