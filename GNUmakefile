GO ?= go
SRC_DIR := src
PROVIDER := terraform-provider-omniddi
VERSION ?= $(shell cat VERSION)
TF_VERSION ?= 1.17.1
GOVULNCHECK_VERSION ?= v1.9.0
GITLEAKS_VERSION ?= v8.30.1

.DEFAULT_GOAL := build
.PHONY: build install fmt lint test testacc docs docs-check vuln secrets-scan tidy version-check release-notes changelog-date

build:
	cd $(SRC_DIR) && $(GO) build -trimpath -ldflags "-X main.version=$(VERSION)" -o $(PROVIDER) .

install:
	cd $(SRC_DIR) && $(GO) install -trimpath -ldflags "-X main.version=$(VERSION)" .

fmt:
	cd $(SRC_DIR) && gofmt -s -w .

lint:
	cd $(SRC_DIR) && golangci-lint run ./...

test:
	cd $(SRC_DIR) && $(GO) test ./... -cover -timeout 10m

testacc:
	cd $(SRC_DIR) && TF_ACC=1 $(GO) test ./... -v -timeout 120m

docs:
	cd $(SRC_DIR) && $(GO) run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs generate \
		--provider-dir . \
		--provider-name omniddi \
		--examples-dir ../examples \
		--website-source-dir ../templates \
		--rendered-website-dir ../docs \
		$(if $(shell command -v terraform),,--tf-version $(TF_VERSION))

docs-check: docs
	git diff --exit-code -- docs/
	test -z "$$(git ls-files --others --exclude-standard -- docs/)"

vuln:
	cd $(SRC_DIR) && $(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

secrets-scan:
	$(GO) run github.com/zricethezav/gitleaks/v8@$(GITLEAKS_VERSION) git --redact --verbose --no-banner --exit-code 1 --log-opts=HEAD .

tidy:
	cd $(SRC_DIR) && $(GO) mod tidy

version-check:
	@grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$$' VERSION || { echo "VERSION must hold one semantic version, got '$(VERSION)'" >&2; exit 1; }
	@grep -q '^## $(VERSION) ' CHANGELOG.md || { echo "CHANGELOG.md has no '## $(VERSION)' section" >&2; exit 1; }
	@test -z "$(TAG)" || test "$(TAG)" = "v$(VERSION)" || { echo "Tag $(TAG) does not match VERSION $(VERSION); expected v$(VERSION)" >&2; exit 1; }
	@test -z "$(TAG)" || ! grep -q '^## $(VERSION) (Unreleased)' CHANGELOG.md || { echo "CHANGELOG.md still marks $(VERSION) as Unreleased" >&2; exit 1; }

release-notes:
	@awk -v heading="## $(VERSION) " 'index($$0, "## ") == 1 { if (found) exit; found = (index($$0, heading) == 1); next } found' CHANGELOG.md | sed -e '/./,$$!d'

changelog-date:
	@grep -q '^## $(VERSION) (Unreleased)$$' CHANGELOG.md || { echo "CHANGELOG.md has no '## $(VERSION) (Unreleased)' heading to date" >&2; exit 1; }
	@sed -i.bak 's/^## $(VERSION) (Unreleased)$$/## $(VERSION) ($(shell date -u +%Y-%m-%d))/' CHANGELOG.md && rm CHANGELOG.md.bak
	@grep '^## $(VERSION) ' CHANGELOG.md
