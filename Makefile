.PHONY: up
## up: runs application
up:
	@go run $$(ls | grep -v _test.go | grep .go)

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

.PHONY: help
## help: prints help message
help:
	@echo "Consul LB GCE"
	@echo
	@echo "Usage:"
	@echo
	@sed -n 's/^##//p' ${MAKEFILE_LIST} | column -t -s ':' | sed -e 's/^/ /'
	@echo

