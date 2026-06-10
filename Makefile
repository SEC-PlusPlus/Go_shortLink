.PHONY: run build test clean tidy

# Run the application
run:
	go run main.go -config=config/config.yaml

# Build the binary
build:
	go build -o bin/shortlink main.go

# Run tests
test:
	go test ./... -v -count=1

# Download dependencies
tidy:
	go mod tidy

# Clean build artifacts
clean:
	rm -rf bin/
