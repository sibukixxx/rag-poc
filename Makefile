.PHONY: build test vet run doctor init clean docker-build up down logs

build:
	CGO_ENABLED=0 go build -o dist/forgeai ./cmd/forgeai

test:
	CGO_ENABLED=0 go test ./...

vet:
	go vet ./...

run: build
	./dist/forgeai serve

doctor: build
	./dist/forgeai doctor

init: build
	./dist/forgeai init

clean:
	rm -rf dist data

# --- Docker / Cloudflare Tunnel (docs/deploy-cloudflare.md) ---
IMAGE ?= forgeai:local
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

docker-build:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE) .

up:
	docker compose up -d --build

down:
	docker compose down

logs:
	docker compose logs -f
