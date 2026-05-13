all-notebook: build generate-inputs run-notebook

all-system: build generate-compose generate-inputs run-system

compare:
	docker run --rm \
		-v $(PWD)/output:/app/output \
		money-laundering python scripts/compare_outputs.py

build:
	docker build -t money-laundering .

generate-compose:
	docker run --rm \
		--env-file .env \
		-v $(PWD)/system:/app/system \
		money-laundering python scripts/generate_compose.py

generate-inputs:
	mkdir -p input
	docker run --rm \
		--env-file .env \
		-v $(PWD)/data:/app/data \
		-v $(PWD)/input:/app/input \
		money-laundering python scripts/generate_inputs.py

run-notebook:
	mkdir -p output/notebook
	docker run --rm \
		--env-file .env \
		-v $(PWD)/input:/app/input \
		-v $(PWD)/output:/app/output \
		money-laundering python scripts/run_analysis.py

run-system:
	docker compose -f system/docker-compose.yml up --build

stop-system:
	docker compose -f system/docker-compose.yml down

down:
	docker stop $$(docker ps -q --filter ancestor=money-laundering) 2>/dev/null || true
