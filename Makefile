BINS=guardduplicate parsefile archive logimporterror

build:
        mkdir -p bin
        @for b in $(BINS); do \
                GOOS=linux GOARCH=arm64 go build -o bin/$$b cmd/$$b/main.go; \
        done

sam-deploy-dev:
	sam build && sam deploy --config-env dev

sam-local-test:
	sam local invoke GuardDuplicate --event testdata/s3_event.json
