.PHONY: all build clean

all: build

build:
	@echo "Building the application..."
	@mkdir -p build
	@go build -o build/myapp main.go

install:
	@echo "Installing the application..."
	@go install
