.PHONY: build test lint clean lab-deploy lab-destroy lab-verify lab-verify-full lab-status lab-logs help

BINARY    := rift-srl
MODULE    := ./cmd/rift-srl/
GOFLAGS   := CGO_ENABLED=0 GOOS=linux GOARCH=amd64
LAB_TOPO  := lab/topology.clab.yml
LAB_NAME  := rift
NODES     := spine1 spine2 leaf1 leaf2 leaf3

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-20s %s\n", $$1, $$2}'

# ---------- Build & Test ----------

build: ## Build the rift-srl binary
	$(GOFLAGS) go build -o $(BINARY) $(MODULE)

test: ## Run all Go tests
	go test ./...

lint: ## Run go vet
	go vet ./...

clean: ## Remove build artifacts
	rm -f $(BINARY)

# ---------- Lab Lifecycle ----------

lab-deploy: ## Deploy the containerlab topology
	containerlab deploy -t $(LAB_TOPO)

lab-destroy: ## Destroy the containerlab topology
	containerlab destroy -t $(LAB_TOPO) --cleanup

lab-push: build ## Build and push agent to all lab nodes
	bash lab/scripts/deploy.sh

# ---------- Lab Verification ----------

lab-verify: ## Run basic verification checks
	bash lab/scripts/verify.sh

lab-verify-full: ## Run full verification including disaggregation test
	bash lab/scripts/verify.sh --full

# ---------- Lab Inspection ----------

lab-status: ## Show agent status on all nodes
	@for node in $(NODES); do \
		echo "=== $$node ==="; \
		docker exec clab-$(LAB_NAME)-$$node sr_cli -- "info from state system app-management application rift-srl state" 2>/dev/null || echo "  not reachable"; \
		echo ""; \
	done

lab-logs: ## Show agent logs on all nodes
	@for node in $(NODES); do \
		echo "=== $$node ==="; \
		docker exec clab-$(LAB_NAME)-$$node cat /var/log/srlinux/stdout/rift-srl.log 2>/dev/null | tail -20 || echo "  no logs"; \
		echo ""; \
	done

lab-adj: ## Show RIFT adjacency state on all nodes
	@for node in $(NODES); do \
		echo "=== $$node ==="; \
		docker exec clab-$(LAB_NAME)-$$node sr_cli -- "info from state rift interface *" 2>/dev/null || echo "  no RIFT state"; \
		echo ""; \
	done

lab-routes: ## Show RIFT routes on all nodes
	@for node in $(NODES); do \
		echo "=== $$node ==="; \
		docker exec clab-$(LAB_NAME)-$$node sr_cli -- "info from state network-instance default route-table ipv4-unicast" 2>/dev/null || echo "  no routes"; \
		echo ""; \
	done
