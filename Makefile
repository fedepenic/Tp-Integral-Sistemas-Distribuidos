build:
	docker build -t money-laundering .

generate-inputs:
	mkdir -p input
	docker run --rm \
		--env-file .env \
		-v $(PWD)/data:/app/data \
		-v $(PWD)/input:/app/input \
		money-laundering python generate_inputs.py

run:
	mkdir -p output
	docker run --rm \
		--env-file .env \
		-v $(PWD)/input:/app/input \
		-v $(PWD)/output:/app/output \
		money-laundering python run_analysis.py

down:
	docker stop $$(docker ps -q --filter ancestor=money-laundering) 2>/dev/null || true

all: build generate-inputs run
