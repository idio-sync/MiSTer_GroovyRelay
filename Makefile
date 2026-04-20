.PHONY: build build-bridge build-fake test test-integration lint clean

build: build-bridge build-fake

build-bridge:
	go build -o mister-groovy-relay ./cmd/mister-groovy-relay

build-fake:
	go build -o fake-mister ./cmd/fake-mister

test:
	go test ./...

test-integration:
	go test -tags=integration ./tests/integration/...

lint:
	go vet ./...

clean:
	rm -f mister-groovy-relay fake-mister
