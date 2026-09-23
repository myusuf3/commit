.PHONY: build test vet fmt check clean

build:
	go build -trimpath -o bin/commit ./cmd/commit

test:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -w cmd internal

check: vet test

clean:
	rm -rf bin dist coverage.out
