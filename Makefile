IMAGE    := spoty-lls
ENV_FILE := .env
GO_IMAGE := golang:1.27

.PHONY: build run token test lint fmt ci

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

ci: build
	docker run --rm -v "$(CURDIR)":/src -w /src $(GO_IMAGE) \
		sh -ec 'echo "==> gofmt";\
			unformatted=$$(gofmt -l .); if [ -n "$$unformatted" ];\
			then echo "gofmt needed for:"; echo "$$unformatted"; exit 1;\
			fi;\
			echo "==> go vet";\
			go vet ./...;\
			echo "==> go test";\
			go test -coverprofile=coverage.out -covermode=atomic ./...;\
			echo "==> coverage";\
			go tool cover -func=coverage.out'
