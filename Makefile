run:
	go run ./cmd/syrogo -config ./configs/config.example.yaml

build:
	go build -o ./bin/syrogo ./cmd/syrogo

fmt:
	goimports -w ./cmd ./internal

test:
	go test ./...
