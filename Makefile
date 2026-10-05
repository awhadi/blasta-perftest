BINARY  := blasta
VERSION := $(shell sed -n 's/.*Version = "\(.*\)"/\1/p' internal/version/version.go)
LDFLAGS := -s -w

.PHONY: build run test race vet fmt clean install

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/blasta

install:
	CGO_ENABLED=0 go install -trimpath -ldflags "$(LDFLAGS)" ./cmd/blasta

run: build
	./$(BINARY) serve

test:
	go test ./... -count=1

race:
	go test ./... -race -count=1

vet:
	go vet ./...
	gofmt -l . | tee /dev/stderr | (! read)

fmt:
	gofmt -w .

clean:
	rm -f $(BINARY)

.PHONY: docker-build docker-up docker-down docker-logs

docker-build:
	docker compose build

docker-up:
	docker compose up -d --build

docker-down:
	docker compose down

docker-logs:
	docker compose logs -f blasta
