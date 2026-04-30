SHELL := /bin/bash

.PHONY: test vet check dev-up dev-down api smtp

test:
	go test ./...

vet:
	go vet ./...

check: test vet
	node --check web/main.js
	node --check web/login.js

dev-up:
	docker compose up -d

dev-down:
	docker compose down

api:
	go run ./cmd/api

smtp:
	go run ./cmd/smtp
