BINS=guardduplicate parsefile archive logimporterror

.PHONY: build build-% sam-deploy-dev sam-local-test

build:
	mkdir -p bin
	@for b in $(BINS); do \
	GOOS=linux GOARCH=arm64 go build -tags lambda -o bin/$$b ./cmd/$$b; \
	done

sam-deploy-dev:
	sam build && sam deploy --config-env dev

sam-local-test:
	sam local invoke GuardDuplicate --event testdata/s3_event.json

build-%:
	$(MAKE) build

up:
	docker-compose up -d

test:
	E2E=1 go test ./tests/e2e -v -coverprofile=e2e-cover.out

down:
	docker-compose down
