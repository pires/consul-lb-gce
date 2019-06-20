.EXPORT_ALL_VARIABLES:

GOPATH=$(shell pwd)

all: clean build

.PHONY: install
install:
	@cd src/github.com/pires/consul-lb-google; glide install

build:
	@cd src/github.com/pires/consul-lb-google; go build

release:
	@cd src/github.com/pires/consul-lb-google; GOOS=linux GOARCH=amd64 go build

.PHONY: clean
clean:
	@rm -f src/github.com/pires/consul-lb-google/consul-lb-google
	@gofmt -s -w src

.PHONY: test
test:
	@cd src/github.com/pires/consul-lb-google; go test -v
