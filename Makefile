.PHONY: test check-fast check-full test-container esp-idf backend e2e e2e-container backend-adk-gate

test: check-fast

check-fast:
	bash scripts/check_fast.sh

check-full:
	bash scripts/check_full.sh

test-container:
	docker compose run --build --rm test

esp-idf:
	docker compose build esp-idf

backend:
	docker compose up --build backend

e2e:
	bash scripts/e2e.sh

e2e-container:
	docker compose -f compose.e2e.yaml run --build --rm e2e

backend-adk-gate:
	@cd backend && version="$$(GOTOOLCHAIN=local go env GOVERSION)"; \
	if [ "$$version" != "go1.26.6" ]; then \
		echo "ADK gate requires go1.26.6; got $$version" >&2; exit 1; \
	fi; \
	go test -tags "adk,mcp,nolibopusfile" -race -count=1 ./...
