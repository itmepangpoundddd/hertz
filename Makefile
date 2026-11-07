.PHONY: build test vet run loadgen token clean

build:
	go build -o bin/hertz ./cmd/hertz
	go build -o bin/loadgen ./cmd/loadgen
	go build -o bin/token ./cmd/token

test:
	go test ./... -count=1

vet:
	go vet ./...

run:
	HERTZ_ALLOW_INSECURE_DEV=true \
	HERTZ_AUTH_SECRET=0123456789abcdef0123456789abcdef \
	HERTZ_INGEST_ADDR=:5004 \
	HERTZ_ICE_UDP_PORT=8443 \
	HERTZ_ICE_LOOPBACK=true \
	./bin/hertz

loadgen:
	./bin/loadgen -url http://127.0.0.1:8080 -stream main -listeners 100 -ramp 20 -duration 60s

token:
	./bin/token -secret 0123456789abcdef0123456789abcdef -sub listener-1 -stream main -ttl 15m

clean:
	rm -rf bin
