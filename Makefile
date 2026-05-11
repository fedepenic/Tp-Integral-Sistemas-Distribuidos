build:
	docker build -t money-laundering .

run:
	mkdir -p output
	docker run --rm -v $(PWD)/data:/app/data -v $(PWD)/output:/app/output money-laundering

down:
	docker stop $$(docker ps -q --filter ancestor=money-laundering) 2>/dev/null || true

all: build run
