PYTHON ?= python3.11

all: build clean-output generate-compose generate-inputs run-notebook run-system

all-notebook: build clean-output generate-inputs run-notebook

all-notebook-local: clean-output-local generate-inputs-local run-notebook-local

all-system: build clean-output generate-compose generate-inputs run-system

all-system-demo: build clean-output generate-compose clean-client-progress copy-results-to-notebook-local medium run-system

clean-output:
	docker run --rm \
		-v $(PWD)/output:/app/output \
		money-laundering python scripts/clean_output.py

clean-output-local:
	$(PYTHON) scripts/clean_output.py

clean-client-progress:
	mkdir -p input
	docker run --rm \
		-v $(PWD)/scripts:/app/scripts \
		-v $(PWD)/input:/app/input \
		money-laundering python scripts/clean_client_progress.py

clean-client-progress-local:
	mkdir -p input
	$(PYTHON) scripts/clean_client_progress.py

compare:
	docker run --rm \
		-v $(PWD)/output:/app/output \
		money-laundering python scripts/compare_outputs.py $(word 2,$(MAKECMDGOALS))

copy-results-to-notebook:
	mkdir -p output/notebook
	docker run --rm \
		--env-file .env \
		-v $(PWD)/data:/app/data \
		-v $(PWD)/output:/app/output \
		-v $(PWD)/scripts:/app/scripts \
		money-laundering python scripts/copy_results_to_notebook.py $(word 2,$(MAKECMDGOALS))

copy-results-to-notebook-local:
	mkdir -p output/notebook
	set -a && . ./.env && $(PYTHON) scripts/copy_results_to_notebook.py $(word 2,$(MAKECMDGOALS))

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

run-system: stop-system
	docker-compose -f system/docker-compose.yml up --build --remove-orphans

stop-system:
	docker-compose -f system/docker-compose.yml down -v

prune:
	docker image prune -f

down:
	docker stop $$(docker ps -q --filter ancestor=money-laundering) 2>/dev/null || true

kill:
	docker-compose -f system/docker-compose.yml kill -s SIGKILL $(word 2,$(MAKECMDGOALS))

chaos:
	set -a && . ./.env && $(PYTHON) scripts/chaos.py

%:
	@:
