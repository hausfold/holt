VERSION := $(shell cat VERSION)
LDFLAGS := -X github.com/hausfold/holt/internal/commands.Version=$(VERSION)

.PHONY: build test fmt vet check clean score

build:
	go build -ldflags "$(LDFLAGS)" -o holt ./cmd/holt

# The acceptance suite. It is black-box — it drives the built binary with shim
# gh/lsof on PATH — so it is the same suite the bash `wt` runs against, and
# WT_UNDER_TEST still points it at any other implementation for comparison.
#
# `go test` runs first, and covers the one thing a black-box suite structurally
# can't: code that edits a file belonging to ANOTHER tool (Claude Code's
# ~/.claude.json), where most of the assertion is about what came through the
# rewrite untouched.
test: build
	go test ./...
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
