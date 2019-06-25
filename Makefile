.EXPORT_ALL_VARIABLES:

GOPATH=$(shell pwd)

all: clean build test

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
	@gofmt -s -w src/github.com/pires/consul-lb-google

.PHONY: test
test:
	@go test github.com/pires/consul-lb-google/util github.com/pires/consul-lb-google/tagparser github.com/pires/consul-lb-google/cloud/gce

