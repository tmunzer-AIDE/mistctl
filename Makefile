# mistctl developer and release tasks. Run `make help` for the list.
#
# Release targets drive the GitHub CLI (gh) logged in as the repository owner.
# See RELEASING.md for what each secret is and where it comes from.

GORELEASER ?= goreleaser
RELEASE := ./scripts/release.sh

.DEFAULT_GOAL := help

.PHONY: help build test lint check snapshot \
	release-doctor release-repos release-tokens release-secrets-macos \
	release-secrets-windows release-setup release publish winget-pr

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
	$(GORELEASER) release --snapshot --clean --skip=sign,notarize,sbom

## One-time release setup

release-doctor: ## Show which repos and secrets are set up
	@$(RELEASE) doctor

release-repos: ## Create homebrew-tap and scoop-bucket, fork winget-pkgs
	@$(RELEASE) repos

release-tokens: ## Create the two PATs in the browser and store them as secrets
	@$(RELEASE) tokens

release-secrets-macos: ## Store Apple secrets: P12= P8= KEY_ID= ISSUER_ID= [P12_PASSWORD=]
	@P12="$(P12)" P8="$(P8)" KEY_ID="$(KEY_ID)" ISSUER_ID="$(ISSUER_ID)" $(RELEASE) secrets-macos

release-secrets-windows: ## Store Windows signing secrets: PFX= [PFX_PASSWORD=]
	@PFX="$(PFX)" $(RELEASE) secrets-windows

release-setup: release-repos release-tokens release-doctor ## Repos + tokens, then show what is left

## Releasing

release: ## Tag main as VERSION and push it (draft release): VERSION=v0.1.0
	@$(RELEASE) tag "$(VERSION)"

publish: ## Publish the draft release: VERSION=v0.1.0
	@$(RELEASE) publish "$(VERSION)"

winget-pr: ## Open the winget-pkgs PR after publishing: VERSION=v0.1.0
	@$(RELEASE) winget-pr "$(VERSION)"
