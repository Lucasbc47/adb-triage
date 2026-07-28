BINARY := adb-triage$(shell go env GOEXE)

.PHONY: build run test check clean fmt-seed

build:
	go build -o $(BINARY) .

run: build
	./$(BINARY)

test:
	go test ./...

# What CI should run. gofmt -l prints offending files and exits 0, so the
# output has to be checked explicitly for it to fail a build.
check:
	@test -z "$$(gofmt -l .)" || { echo "gofmt needed:"; gofmt -l .; exit 1; }
	go vet ./...
	go test ./...

fmt-seed:
	go run ./cmd/seedfmt

clean:
	rm -f adb-triage adb-triage.exe