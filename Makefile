BINARY := ag
GOFLAGS := -ldflags="-s -w"

.PHONY: build test test-v test-integration vet clean install

build:
	go build $(GOFLAGS) -o $(BINARY) .

test:
	go test ./...

test-v:
	go test ./... -v

test-integration:
	@scripts/test-integration.sh

vet:
	go vet ./...

clean:
	rm -f $(BINARY)

install:
	go install $(GOFLAGS) .
