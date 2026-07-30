IMAGE    := spoty-lls
ENV_FILE := .env

.PHONY: build build-test run token test lint fmt

build:
	docker build -t $(IMAGE) .

build-test:
	docker build --target test -t $(IMAGE)-test .

run: build
	docker run --env-file $(ENV_FILE) $(IMAGE)

token: build
	docker run -it --env-file $(ENV_FILE) -p 8888:8888 --entrypoint python $(IMAGE) get_token.py

test: build-test
	docker run --rm $(IMAGE)-test pytest

lint: build-test
	docker run --rm $(IMAGE)-test ruff check .

fmt: build-test
	docker run --rm -v $(PWD):/work -w /work $(IMAGE)-test ruff format .
	docker run --rm -v $(PWD):/work -w /work $(IMAGE)-test ruff check --fix .