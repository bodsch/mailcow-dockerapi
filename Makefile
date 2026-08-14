BINARY  := dockerapi
IMAGE   := mailcow-dockerapi
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

GO      ?= go
GOFLAGS := -trimpath

.DEFAULT_GOAL := check

.PHONY: check
check: fmt vet test ## Formatierung, Prüfer und Tests

.PHONY: build
build: ## Binary bauen
	CGO_ENABLED=0 $(GO) build $(GOFLAGS) -ldflags="-s -w -X main.version=$(VERSION)" -o $(BINARY) ./cmd/dockerapi

.PHONY: fmt
fmt: ## Formatierung prüfen
	@out="$$(gofmt -l cmd internal)"; \
	if [ -n "$$out" ]; then echo "nicht formatiert:"; echo "$$out"; exit 1; fi

.PHONY: vet
vet: ## Statische Prüfung
	$(GO) vet ./...

.PHONY: test
test: ## Tests mit Wettlauferkennung
	$(GO) test ./... -race -cover

.PHONY: test-integration
test-integration: ## Tests gegen einen echten Docker-Daemon
	$(GO) test -tags=integration ./... -race -count=1

.PHONY: fuzz
fuzz: ## Fuzz-Tests für Maskierung und Stromzerlegung (30s je Ziel)
	$(GO) test ./internal/actions/ -run=NONE -fuzz=FuzzShellQuote -fuzztime=30s
	$(GO) test ./internal/dockerclient/ -run=NONE -fuzz=FuzzDemux -fuzztime=30s

.PHONY: compare
compare: ## Antworten gegen die Python-Fassung vergleichen (braucht Docker)
	./scripts/compare-with-python.sh

.PHONY: cover
cover: ## Abdeckungsbericht im Browser
	$(GO) test ./... -coverprofile=coverage.out
	$(GO) tool cover -html=coverage.out

.PHONY: image
image: ## Container-Abbild bauen
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) -t $(IMAGE):latest .

.PHONY: tidy
tidy: ## Abhängigkeiten aufräumen
	$(GO) mod tidy

.PHONY: clean
clean: ## Erzeugnisse entfernen
	rm -f $(BINARY) coverage.out

.PHONY: help
help: ## Diese Übersicht
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
	  | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'
