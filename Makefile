.PHONY: run test cleanup build

run:
	go run ./cmd/app

build:
	go build -o bin/minipress ./cmd/app

test:
	go vet ./... && go test -race -cover ./...

cleanup:
	go run ./cmd/app -cleanup-now
