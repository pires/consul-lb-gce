.PHONY: up
## up: runs application
up:
	@go run $(ls | grep -v _test.go | grep .go)

.PHONY: release
## release: builds application for linux
release:
	@GOOS=linux GOARCH=amd64 go build

.PHONY: fmt
## fmt: formats source code
fmt:
	@gofmt -s -w .

.PHONY: test
## test: runs tests
test:
	@go test -v  ./...

