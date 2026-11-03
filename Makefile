.PHONY: all build test race bench lint clean docker-build

all: build

build:
	go build -o bin/genesis ./cmd/server

test:
	go test -v ./...

race:
	go test -v -race ./...

bench:
	go test -v -run=TestGenesisBenchmarks ./test/bench

lint:
	go vet ./...
	@if command -v staticcheck >/dev/null 2>&1; then \
		echo "Running staticcheck..."; \
		staticcheck ./...; \
	else \
		echo "staticcheck not installed, skipping (install with: go install honnef.co/go/tools/cmd/staticcheck@latest)"; \
	fi

clean:
	rm -rf bin/

docker-build:
	docker build -t genesis:latest .
