BINARY := adb-triage$(shell go env GOEXE)

.PHONY: build run clean fmt-seed

build:
	go build -o $(BINARY) .

run: build
	./$(BINARY)

fmt-seed:
	go run ./cmd/seedfmt

clean:
	rm -f adb-triage adb-triage.exe