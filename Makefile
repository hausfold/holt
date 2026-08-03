VERSION := $(shell cat VERSION)
LDFLAGS := -X github.com/nebelhaus/holt/internal/commands.Version=$(VERSION)

.PHONY: build test fmt vet check clean score

build:
	go build -ldflags "$(LDFLAGS)" -o holt ./cmd/holt

# The acceptance suite. It is black-box — it drives the built binary with shim
# gh/lsof on PATH — so it is the same suite the bash `wt` runs against, and
# WT_UNDER_TEST still points it at any other implementation for comparison.
test: build
	bats test/holt.bats

# What fraction of the 0.1 contract holds today. Every remaining failure should
# be an unimplemented command, never a wrong behaviour in an implemented one.
score: build
	@bats test/holt.bats 2>&1 | grep -c '^ok ' | tr -d ' ' | xargs -I{} echo "{} / $$(grep -c '^@test' test/holt.bats) passing"
	@bats test/holt.bats 2>&1 | grep '^not ok' | sed 's/^not ok [0-9]* //;s/:.*//' | sort | uniq -c | sort -rn

fmt:
	gofmt -w ./cmd ./internal

vet:
	go vet ./...

check: fmt vet test

clean:
	rm -rf holt .gocache dist
