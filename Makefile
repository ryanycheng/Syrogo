run:
	go run ./cmd/syrogo -config ./configs/config.yaml -dev-log

dev:
	air -c .air.toml

build:
	go build -o ./bin/syrogo ./cmd/syrogo

lint:
	golangci-lint run

fmt:
	goimports -w ./cmd ./internal

test:
	go test ./...

update-pricing:
	./scripts/update_pricing.py

smoke:
	./scripts/smoke.sh

e2e:
	./scripts/e2e.py
