# Makefile for the ask-ai project.

ifeq ($(GOPATH),)
    GOPATH := $(HOME)/go
    $(info GOPATH not set, using default: $(GOPATH))
endif

BINARY_DIR := bin
INSTALL_DIR := $(GOPATH)/bin
MAIN_BINARY := ask-ai

CMD_FILES := $(wildcard cmd/**/*.go)
BIN_FILES := $(patsubst cmd/%/main.go,%,$(CMD_FILES))
PKG_FILES := $(wildcard pkg/**/*.go)
TST_FILES := $(wildcard pkg/**/*_test.go)
TST_DIRS  := $(shell go list ./... | grep -v cmd)

CP := $(shell which cp)
GO := $(shell which go)
GOCYCLO := $(shell which gocyclo 2>/dev/null)

CPFLAGS := -p
# GOFLAGS := -ldflags "-X 'github.com/duluk/ask-ai/pkg/config.commit=$(shell git rev-parse --short HEAD)' -X 'github.com/duluk/ask-ai/pkg/config.date=$(shell date -u '+%Y-%m-%d %H:%M:%S')'"
TESTFLAGS := -cover -coverprofile=coverage.out

$(shell mkdir -p $(BINARY_DIR))

all: check build

build: $(addprefix $(BINARY_DIR)/,$(BIN_FILES))

$(BINARY_DIR)/%: cmd/%/main.go $(PKG_FILES)
	$(GO) build $(GOFLAGS) -o $@ $<

list:
	@echo "CMD_FILES: $(CMD_FILES)"
	@echo "BIN_FILES: $(BIN_FILES)"
	@echo "PKG_FILES: $(PKG_FILES)"
	@echo "TST_FILES: $(TST_FILES)"

clean:
	rm -rf $(BINARY_DIR)/* coverage.out

# For verbose, run `make test VERBOSE=1` (or put VERBOSE=1 first); I'm not sure
# how to pass `-v` from the CLI to this
test: $(TST_FILES)
	@echo "Running Go tests..."
	$(GO) test $(TESTFLAGS) $(if $(VERBOSE),-v) $(TST_DIRS) || exit 1
	@if [ -x "$(GOCYCLO)" ]; then \
		echo -e "\nRunning cyclomatic complexity test..." ; \
		$(GOCYCLO) --over 12 . || exit 0 ; \
	fi

check: vet

fmt: $(CMD_FILES)
	$(GO) fmt ./...

vet: $(CMD_FILES) fmt
	$(GO) vet ./...

run: $(BINARY_DIR)/$(MAIN_BINARY)
	./$(BINARY_DIR)/$(MAIN_BINARY)

install: all
	@mkdir -p $(INSTALL_DIR)
	@for app in $(BIN_FILES); do \
		$(CP) $(CPFLAGS) $(BINARY_DIR)/$$app $(INSTALL_DIR); \
		echo "Installed $$app to $(INSTALL_DIR)"; \
	done

# Futurte self: This ensures that make treats the targets as labels and not
# files. This is important because if a file of the same name actually exists,
# it may not be executed if the timestamp hasn't changed. That's not what we
# want for these.
.PHONY: all build list check clean test fmt vet run install
