# mistctl developer and release tasks. Run `make help` for the list.
#
# Release targets run on the maintainer's Mac with the GitHub CLI (gh) logged
# in as the repository owner. See RELEASING.md.

GORELEASER ?= goreleaser
RELEASE := ./scripts/release.sh

.DEFAULT_GOAL := help

.PHONY: help build test lint check snapshot \
	release-doctor release-repos release publish winget-pr

help: ## Show this help
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  \033[36m%-24s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

## Development

build: ## Build ./mistctl for this machine
	go build -trimpath -o mistctl ./cmd/mistctl

test: ## Run the tests
	go test -race ./...

lint: ## gofmt check and go vet
	@test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)
	go vet ./...

check: ## Validate the GoReleaser config
	$(GORELEASER) check

snapshot: ## Local release dry run into dist/ (no signing, nothing published)
	CODESIGN_IDENTITY= $(GORELEASER) release --snapshot --clean --skip=sign,notarize,sbom

## One-time release setup

release-doctor: ## Check tools, signing identity, notary profile and repos
	@$(RELEASE) doctor

release-repos: ## Create homebrew-tap and scoop-bucket, fork winget-pkgs
	@$(RELEASE) repos

## Releasing (on your Mac)

release: ## Test, tag, sign, notarize and create the draft release: VERSION=v0.1.0
	@$(RELEASE) release "$(VERSION)"

publish: ## Publish the draft release: VERSION=v0.1.0
	@$(RELEASE) publish "$(VERSION)"

winget-pr: ## Open the winget-pkgs PR after publishing: VERSION=v0.1.0
	@$(RELEASE) winget-pr "$(VERSION)"
