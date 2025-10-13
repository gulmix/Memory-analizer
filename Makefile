build:
	go build -o memory-analyzer main.go

clean:
	rm -f memory-analyzer
	rm -rf dist/

install:
	go install

fmt:
	go fmt ./...

run:
	go run main.go