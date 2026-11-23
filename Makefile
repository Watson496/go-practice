SHELL := /bin/sh

BINARY := bin/go-practice
ADDRESS ?= 127.0.0.1:8080
DATA_DIR ?= data/games
SERVER_ARGS ?=
GO_FILES := $(shell find . -type f -name '*.go' -not -path './.git/*')
GO_SOURCES := $(GO_FILES) go.mod

.PHONY: help setup build build.server build.web run run.server run.web test test.server test.web check check.server check.web lint.web typecheck.web format.web clean

help:
	@printf '%s\n' \
		'make setup         フロントエンドの依存関係を導入する' \
		'make build         Goの実行ファイルと本番用フロントエンドをビルドする' \
		'make build.server  bin/go-practiceをビルドする' \
		'make build.web     フロントエンドをビルドする' \
		'make run           ビルド済みのGo Practiceを起動する' \
		'make run.server    ビルド済みのAPIサーバーだけを起動する' \
		'make run.web       Viteの開発サーバーを起動する' \
		'make test          Goとフロントエンドのテストを実行する' \
		'make test.server   Goのテストをデータ競合検査付きで実行する' \
		'make test.web      フロントエンドのテストを実行する' \
		'make check         テストとGo・フロントエンドの静的検査・整形検査・型検査を実行する' \
		'make check.server  Goの整形検査と静的検査を実行する' \
		'make check.web     フロントエンドの静的検査・整形検査を実行する' \
		'make lint.web      フロントエンドを静的検査する' \
		'make typecheck.web フロントエンドを型検査する' \
		'make format.web    フロントエンドを整形する' \
		'make clean         ローカルのビルド成果物を削除する'

setup:
	pnpm --dir web install

build: build.server build.web

build.server: $(BINARY)

$(BINARY): $(GO_SOURCES)
	@mkdir -p bin
	go build -o $(BINARY) ./cmd/go-practice

build.web:
	pnpm --dir web build

run:
	./$(BINARY) serve --address $(ADDRESS) --data-dir $(DATA_DIR) $(SERVER_ARGS)

run.server:
	./$(BINARY) serve --api-only --address $(ADDRESS) --data-dir $(DATA_DIR) $(SERVER_ARGS)

run.web:
	pnpm --dir web dev

test: test.server test.web

test.server:
	go test -race ./...

test.web:
	pnpm --dir web test

check: test check.server check.web typecheck.web

check.server:
	@unformatted="$$(gofmt -l $(GO_FILES))"; if [ -n "$$unformatted" ]; then printf '%s\n' "$$unformatted"; exit 1; fi
	go vet ./...
	go tool deadcode -test ./...
	go tool staticcheck ./...

check.web:
	pnpm --dir web check

lint.web:
	pnpm --dir web lint

typecheck.web:
	pnpm --dir web typecheck

format.web:
	pnpm --dir web format

clean:
	rm -f $(BINARY)
	rm -rf web/dist
