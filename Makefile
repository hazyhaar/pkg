.PHONY: build test

build:
	CGO_ENABLED=0 go build -o cmd/sas_ingester/bin/sas_ingester ./cmd/sas_ingester/

test:
	go test -race -v -count=1 -timeout 120s ./...
