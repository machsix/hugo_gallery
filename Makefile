.PHONY: build clean

# Binary name
BINARY=hugo_gallery

# Build the binary
build:
	go build -o $(BINARY) .

# Clean build artifacts
clean:
	rm -f $(BINARY)
	go clean

# Install the binary to $GOPATH/bin
install:
	go install .