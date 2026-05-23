.PHONY: test test-race vet lint check check-full install-hooks

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

lint:
	golangci-lint run ./...

check: vet lint test

check-full: check test-race

install-hooks:
	git config core.hooksPath scripts/hooks
