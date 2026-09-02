.PHONY: build test vet run doctor init clean

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
