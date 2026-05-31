PYTHON ?= python3.11

all: build clean-output generate-compose generate-inputs run-notebook run-system

all-notebook: build clean-output generate-inputs run-notebook

all-notebook-local: clean-output-local generate-inputs-local run-notebook-local

all-system: build clean-output generate-compose generate-inputs run-system

all-system-demo: build generate-compose run-system

clean-output:
	docker run --rm \
		-v $(PWD)/output:/app/output \
		money-laundering python scripts/clean_output.py

clean-output-local:
	$(PYTHON) scripts/clean_output.py

compare:
	docker run --rm \
		-v $(PWD)/output:/app/output \
		money-laundering python scripts/compare_outputs.py $(word 2,$(MAKECMDGOALS))

build:
	docker build -t money-laundering .

generate-compose:
	docker run --rm \
		--env-file .env \
		-v $(PWD)/system:/app/system \
		-v $(PWD)/scripts:/app/scripts \
		money-laundering python scripts/generate_compose.py

generate-inputs:
	mkdir -p input
	docker run --rm \
		--env-file .env \
		-v $(PWD)/data:/app/data \
		-v $(PWD)/input:/app/input \
		money-laundering python scripts/generate_inputs.py

generate-inputs-local:
	mkdir -p input
	set -a && . ./.env && $(PYTHON) scripts/generate_inputs.py

run-notebook:
	mkdir -p output/notebook
	docker run --rm \
		--env-file .env \
		-v $(PWD)/input:/app/input \
		-v $(PWD)/output:/app/output \
		money-laundering python scripts/run_analysis.py

run-notebook-local:
	mkdir -p output/notebook
	set -a && . ./.env && $(PYTHON) scripts/run_analysis.py

run-system:
	docker-compose -f system/docker-compose.yml up --build --remove-orphans

stop-system:
	docker-compose -f system/docker-compose.yml down -v

down:
	docker stop $$(docker ps -q --filter ancestor=money-laundering) 2>/dev/null || true

.PHONY: 1 2 3 4 5
1 2 3 4 5:
	@:
