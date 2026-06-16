SHELL := /bin/sh

.PHONY: tidy generate manifests test build

tidy:
	go mod tidy

generate:
	go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.17.2 object paths="./..."

manifests:
	mkdir -p config/crd
	go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.17.2 crd:allowDangerousTypes=true paths="./api/..." output:crd:artifacts:config=config/crd

test:
	go test ./...

build:
	go build ./cmd/manager
