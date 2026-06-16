SHELL := /bin/sh

.PHONY: tidy generate manifests test build

tidy:
	go mod tidy

generate:
	go run sigs.k8s.io/controller-tools/cmd/controller-gen object paths="./api/v1alpha1"

manifests:
	mkdir -p config/crd
	go run sigs.k8s.io/controller-tools/cmd/controller-gen crd:allowDangerousTypes=true paths="./api/v1alpha1" output:crd:artifacts:config=config/crd

test:
	go test ./...

build:
	go build ./cmd/manager
