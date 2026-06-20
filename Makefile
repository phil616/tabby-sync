.PHONY: build test test-race vet check docker-build release-check release-snapshot

build:
	CGO_ENABLED=0 go build -trimpath -o tabby-config-sync ./cmd/tabby-config-sync

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

check: vet test test-race

docker-build:
	docker build -t tabby-config-sync:local .

release-check:
	docker run --rm \
		--user "$$(id -u):$$(id -g)" \
		-e HOME=/tmp \
		-v "$(CURDIR):/work" \
		-w /work \
		goreleaser/goreleaser:v2.12.7 check

release-snapshot:
	docker run --rm \
		--user "$$(id -u):$$(id -g)" \
		-e HOME=/tmp \
		-e GOCACHE=/tmp/go-cache \
		-e GOMODCACHE=/tmp/go-mod \
		-v "$(CURDIR):/work" \
		-w /work \
		goreleaser/goreleaser:v2.12.7 release --snapshot --clean
