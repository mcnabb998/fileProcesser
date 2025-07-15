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
