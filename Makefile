run:
	go run ./cmd/syrogo -config ./configs/config.yaml

dev:
	air -c .air.toml

build:
	go build -o ./bin/syrogo ./cmd/syrogo

fmt:
	goimports -w ./cmd ./internal

test:
	go test ./...
