.PHONY: build test test-race e2e

build:
	go build -trimpath -o bin/clipboard-exchange ./cmd/clipboard-exchange

test:
	go test ./...
	go vet ./...

test-race:
	go test -race ./...

e2e:
	npm run test:e2e

