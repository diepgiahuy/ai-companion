.PHONY: test test-container esp-idf backend

test:
	bash scripts/check.sh

test-container:
	docker compose run --build --rm test

esp-idf:
	docker compose build esp-idf

backend:
	docker compose up --build backend

.PHONY: e2e
e2e:
	bash scripts/e2e.sh

.PHONY: e2e-container
e2e-container:
	docker compose -f compose.e2e.yaml run --build --rm e2e

.PHONY: e2e-offline
e2e-offline:
	bash scripts/e2e_offline.sh
