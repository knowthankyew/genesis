.PHONY: all build test race bench clean docker-build

all: build

build:
	go build -o bin/genesis-server ./cmd/server

test:
	go test -v ./...

race:
	go test -v -race ./...

bench:
	go test -v -run=TestGenesisBenchmarks ./test/bench

clean:
	rm -rf bin/

docker-build:
	docker build -t genesis-engine:latest .
