.PHONY: build build-linux test run

build:
	go build -trimpath -o shockstock .

build-linux:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/shockstock-linux-amd64 .

test:
	go test ./...

run:
	go run .
