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

.PHONY: backend-adk-gate
backend-adk-gate:
	@cd backend && version="$$(go env GOVERSION)"; \
	if [ "$$version" != "go1.26.5" ]; then \
		echo "ADK gate requires go1.26.5; got $$version" >&2; exit 1; \
	fi; \
	go test -tags "adk,nolibopusfile" -race -count=1 ./...
