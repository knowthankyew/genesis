.PHONY: all build test race lint bench-report clean docker-build demo-record check-demo-prereqs

all: build

build:
	@mkdir -p bin
	go build -o bin/genesis ./cmd/server
	go build -o bin/genesis-bench ./cmd/genesis-bench

test:
	go test -v ./...

race:
	go test -v -race ./...

lint:
	go vet ./...
	@if command -v staticcheck >/dev/null 2>&1; then \
		echo "Running staticcheck..."; \
		staticcheck ./...; \
	else \
		echo "staticcheck not installed, skipping (install with: go install honnef.co/go/tools/cmd/staticcheck@latest)"; \
	fi

bench-report: build
	@echo "Starting Genesis server in background..."
	@./bin/genesis -tcp=127.0.0.1:9000 -http=127.0.0.1:8080 -max-mem-mb=350 > bin/genesis.log 2>&1 & \
	SERVER_PID=$$!; \
	echo "Server started with PID $$SERVER_PID. Waiting for port ready..."; \
	sleep 1; \
	./bin/genesis-bench -tcp=127.0.0.1:9000 -http=http://127.0.0.1:8080 -duration=60s -clients=50 -query-qps=50 -conns=10000 -server-pid=$$SERVER_PID; \
	STATUS=$$?; \
	echo "Stopping Genesis server (PID $$SERVER_PID)..."; \
	kill -SIGTERM $$SERVER_PID 2>/dev/null || true; \
	exit $$STATUS

clean:
	rm -rf bin/

docker-build:
	docker build -t genesis:latest .

check-demo-prereqs:
	@command -v node >/dev/null 2>&1 || { echo "❌ Node.js is required for demo recording. Please install node." >&2; exit 1; }
	@node -e "try { require('playwright'); } catch { try { require('../node_modules/playwright'); } catch { process.exit(1); } }" 2>/dev/null || { echo "❌ Playwright is required for demo recording. Run 'npm install' in portfolio root." >&2; exit 1; }

demo-record: check-demo-prereqs build
	@./scripts/record-demo.sh
