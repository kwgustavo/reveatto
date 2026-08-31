.PHONY: build clean test

build:
	go build -o glassbox-server ./cmd/glassbox-server
	go build -o gbquery ./cmd/gbquery

clean:
	rm -f glassbox-server gbquery

test:
	go vet ./...
	go build ./...
