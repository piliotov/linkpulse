SERVICES := shortener analytics

.PHONY: test lint build up down kind-up kind-down smoke helm-lint

test:
	@for s in $(SERVICES); do (cd services/$$s && go test -race -cover ./...) || exit 1; done

lint:
	@for s in $(SERVICES); do (cd services/$$s && golangci-lint run) || exit 1; done

build:
	@for s in $(SERVICES); do docker build -t ghcr.io/piliotov/linkpulse-$$s:dev services/$$s || exit 1; done

up:
	docker compose up --build -d

down:
	docker compose down

helm-lint:
	helm lint deploy/helm/linkpulse

kind-up:
	./scripts/kind-deploy.sh

kind-down:
	kind delete cluster --name linkpulse

smoke:
	./scripts/smoke.sh
