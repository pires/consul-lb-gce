GOPATH=$(shell pwd):$(shell pwd)/vendor

all: clean build

build:
	GOARCH=amd64 gb build all

.PHONY: clean
clean:
	@rm -rf ./{bin,pkg}
	@gofmt -s -w src

.PHONY: release
release: clean
	GOOS=linux GOARCH=amd64 gb build -ldflags '-w -extldflags=-static'

.PHONY: test
test:
	@gb test -v

#
# Use COVPKG env var to set which package to run coverage
#
.PHONY: coverage
coverage:
	go test -v -coverprofile cover.out github.com/pires/consul-lb-google/${COVPKG}
	@go tool cover -html=cover.out -o coverage.html
	@rm cover.out
