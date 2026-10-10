default: fmt lint install generate

build:
	go build -v ./...

install: build
	go install -v ./...

lint:
	golangci-lint run

fmt:
	gofmt -s -w -e .

generate:
	go generate ./...

test:
	go test -v -cover -timeout=120s -parallel=10 ./...

# Acceptance tests create REAL resources. Point CLOUDMINATOR_* at a disposable
# organization on a non-production platform.
testacc:
	TF_ACC=1 go test -v -cover -timeout 120m ./...

validate-examples:
	scripts/validate-examples.sh terraform

# Refresh the platform contract the schema test checks against, from a running
# platform (CLOUDMINATOR_ENDPOINT) or a local file/URL (CONTRACT_SRC).
contract:
	@src="$${CONTRACT_SRC:-$${CLOUDMINATOR_ENDPOINT:?set CLOUDMINATOR_ENDPOINT or CONTRACT_SRC}/v1/openapi.json}"; \
	case "$$src" in \
	  http://*|https://*) curl -fsSL "$$src" ;; \
	  *) cat "$$src" ;; \
	esac > internal/contract/openapi.public.json.tmp && \
	mv internal/contract/openapi.public.json.tmp internal/contract/openapi.public.json && \
	echo "updated internal/contract/openapi.public.json from $$src"; \
	go test ./internal/provider/ -run Contract

.PHONY: default build install lint fmt generate test testacc validate-examples contract
