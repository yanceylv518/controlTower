.PHONY: test build build-agent build-server package

# Version injection is intentionally deferred to the future release pipeline.
test:
	go vet ./...
	go test ./...

build: build-agent build-server

build-agent:
	@mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o dist/control-tower-agent-linux-amd64 ./agent/cmd/control-tower-agent
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o dist/control-tower-agent-linux-arm64 ./agent/cmd/control-tower-agent

build-server:
	@mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o dist/control-tower-server-linux-amd64 ./server/cmd/control-tower-server

VERSION ?= dev

package:
	bash deploy/package.sh $(VERSION)
