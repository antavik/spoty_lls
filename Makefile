IMAGE    := spoty-lls
ENV_FILE := .env

.PHONY: build run token test lint fmt

build:
	docker build -t $(IMAGE) .

run: build
	docker run --rm --env-file $(ENV_FILE) $(IMAGE)

token: build
	docker run -it --rm --env-file $(ENV_FILE) --entrypoint /get_token $(IMAGE)

test:
	go test ./...

lint:
	go vet ./...
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt needed for:"; echo "$$unformatted"; exit 1; \
	fi

fmt:
	gofmt -w .
