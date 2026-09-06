VERSION ?= 1.0.0
INSTALLER_VERSION ?= 1.0.0.0
CMAKE_BUILD := build-cmake

.PHONY: all configure test smoke-client smoke-events smoke-input smoke-lifecycle latency-client installer

configure:
	cmake -S . -B $(CMAKE_BUILD) -A x64 -DAPP_VERSION=$(VERSION)

all: configure
	cmake --build $(CMAKE_BUILD) --config Release

test: all
	ctest --test-dir $(CMAKE_BUILD) -C Release --output-on-failure

smoke-client smoke-events smoke-input smoke-lifecycle latency-client: all

installer: test
	makensis /DAPP_VERSION=$(VERSION) /DAPP_VERSION_NUMERIC=$(INSTALLER_VERSION) installer.nsi
