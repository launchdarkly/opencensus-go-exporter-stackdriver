# TODO: Fix this on windows.
ALL_SRC := $(shell find . -name '*.go' \
								-not -path '*/internal/testpb/*' \
								-not -name 'tools.go' \
								-type f | sort)
ALL_PKGS := $(shell go list $(sort $(dir $(ALL_SRC))))

GOTEST_OPT?=-v -race -timeout 30s
GOTEST_OPT_WITH_COVERAGE = $(GOTEST_OPT) -coverprofile=coverage.txt -covermode=atomic
GOTEST=go test
GOFMT=gofmt
GOLINT=golint
GOIMPORTS=goimports
GOVET=go vet
EMBEDMD=embedmd
STATICCHECK=staticcheck
# TODO decide if we need to change these names.
README_FILES := $(shell find . -name '*README.md' | sort | tr '\n' ' ')
GOLANGCI_LINT_VERSION=v1.64.5

LINTER=./bin/golangci-lint
LINTER_VERSION_FILE=./bin/.golangci-lint-version-$(GOLANGCI_LINT_VERSION)

.DEFAULT_GOAL := defaul-goal

.PHONY: defaul-goal
defaul-goal: fmt lint vet embedmd goimports staticcheck test

# TODO: enable test-with-cover when find out why "scripts/check-test-files.sh: 4: set: Illegal option -o pipefail"
.PHONY: ci
ci: fmt lint vet embedmd goimports staticcheck test test-386 test-with-coverage

all-pkgs:
	@echo $(ALL_PKGS) | tr ' ' '\n' | sort

all-srcs:
	@echo $(ALL_SRC) | tr ' ' '\n' | sort

.PHONY: test
test:
	$(GOTEST) $(GOTEST_OPT) $(ALL_PKGS)

.PHONY: test-386
test-386:
	GOARCH=386 $(GOTEST) -v -timeout 30s $(ALL_PKGS)

.PHONY: test-with-coverage
test-with-coverage:
	@echo pre-compiling tests
	@go test -i $(ALL_PKGS)
	$(GOTEST) $(GOTEST_OPT_WITH_COVERAGE) $(ALL_PKGS)
	go tool cover -html=coverage.txt -o coverage.html

.PHONY: test-with-cover
test-with-cover:
	@echo Verifying that all packages have test files to count in coverage
	@scripts/check-test-files.sh $(subst contrib.go.opencensus.io/exporter/stackdriver,./,$(ALL_PKGS))
	@echo pre-compiling tests
	@go test -i $(ALL_PKGS)
	$(GOTEST) $(GOTEST_OPT_WITH_COVERAGE) $(ALL_PKGS)
	go tool cover -html=coverage.txt -o coverage.html

.PHONY: fmt
fmt:
	@FMTOUT=`$(GOFMT) -s -l $(ALL_SRC) 2>&1`; \
	if [ "$$FMTOUT" ]; then \
		echo "$(GOFMT) FAILED => gofmt the following files:\n"; \
		echo "$$FMTOUT\n"; \
		exit 1; \
	else \
	    echo "Fmt finished successfully"; \
	fi

$(LINTER_VERSION_FILE):
	rm -f $(LINTER)
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s $(GOLANGCI_LINT_VERSION)
	touch $(LINTER_VERSION_FILE)

.PHONY: lint
lint: $(LINTER_VERSION_FILE)
	$(LINTER) run ./...

.PHONY: vet
vet:
    # TODO: Understand why go vet downloads "github.com/google/go-cmp v0.2.0"
	@VETOUT=`$(GOVET) ./... | grep -v "go: downloading" 2>&1`; \
	if [ "$$VETOUT" ]; then \
		echo "$(GOVET) FAILED => go vet the following files:\n"; \
		echo "$$VETOUT\n"; \
		exit 1; \
	else \
	    echo "Vet finished successfully"; \
	fi

.PHONY: embedmd
embedmd:
	@EMBEDMDOUT=`$(EMBEDMD) -d $(README_FILES) 2>&1`; \
	if [ "$$EMBEDMDOUT" ]; then \
		echo "$(EMBEDMD) FAILED => embedmd the following files:\n"; \
		echo "$$EMBEDMDOUT\n"; \
		exit 1; \
	else \
	    echo "Embedmd finished successfully"; \
	fi

.PHONY: goimports
goimports:
	@IMPORTSOUT=`$(GOIMPORTS) -d . 2>&1`; \
	if [ "$$IMPORTSOUT" ]; then \
		echo "$(GOIMPORTS) FAILED => fix the following goimports errors:\n"; \
		echo "$$IMPORTSOUT\n"; \
		exit 1; \
	else \
	    echo "Goimports finished successfully"; \
	fi

.PHONY: staticcheck
staticcheck:
	$(STATICCHECK) ./...

.PHONY: install-tools
install-tools:
	GO111MODULE=on go install \
		golang.org/x/lint/golint \
		golang.org/x/tools/cmd/goimports \
		github.com/rakyll/embedmd \
		honnef.co/go/tools/cmd/staticcheck
