.PHONY: run build test migrate-up migrate-down migrate-create

# 加载 .env 中的环境变量
include .env
export

# golang-migrate 二进制路径 (通过 go install 安装)
MIGRATE := $(shell go env GOPATH)/bin/migrate

# 运行开发服务器
# 使用 go build + 直接执行而不是 go run，
# 这样 Ctrl+C 的 SIGINT 能直接被我们的 signal handler 捕获。
run:
	go build -o ./tmp/server ./cmd/server && ./tmp/server

# 编译
build:
	go build -o bin/im-server ./cmd/server

# 测试
test:
	go test ./... -v -count=1

# 数据库迁移
migrate-up:
	$(MIGRATE) -path migrations -database "postgres://$(DB_USER):$(DB_PASSWORD)@$(DB_HOST):$(DB_PORT)/$(DB_NAME)?sslmode=$(DB_SSLMODE)" up

migrate-down:
	$(MIGRATE) -path migrations -database "postgres://$(DB_USER):$(DB_PASSWORD)@$(DB_HOST):$(DB_PORT)/$(DB_NAME)?sslmode=$(DB_SSLMODE)" down

migrate-create:
	$(MIGRATE) create -ext sql -dir migrations -seq $(NAME)

# 代码格式化
fmt:
	go fmt ./...

# 静态检查
vet:
	go vet ./...

# 安装依赖
deps:
	go mod tidy
