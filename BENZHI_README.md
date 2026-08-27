# BENZHI_README

基于 Go 实现的口述史授权净化发布台 Web 项目，一款后端服务，已完整实现口述史资料从授权登记、转写修订、敏感标注、确定性脱敏、逐项复核退回到冻结批准和可验证发布清单的本地浏览器工作台。

## 项目说明
- 项目：benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d
- 项目用途：已完整实现口述史资料从授权登记、转写修订、敏感标注、确定性脱敏、逐项复核退回到冻结批准和可验证发布清单的本地浏览器工作台。
- Go 工具链：`golang:1.22`
- 前端工具链：原生 HTML、CSS 和 JavaScript，由 Go 服务直接提供

## 标准构建、运行和测试命令
进入容器后执行：
cd '/app' && GOTOOLCHAIN=local go build ./...
cd /app && GOTOOLCHAIN=local go run ./cmd/server -addr=127.0.0.1:19091 -selfcheck
cd '/app' && GOTOOLCHAIN=local go test ./...

## Docker 构建和进入容器
chmod +x build_benzhi_docker.sh
./build_benzhi_docker.sh benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d-amd64 linux/amd64
./build_benzhi_docker.sh benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d-arm64 linux/arm64
docker run -it benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d-amd64:latest

## 题目验证命令
1. 预期退出码 0：`go test ./...`
2. 预期退出码 0：`go run ./cmd/server -addr=127.0.0.1:19091 -selfcheck`
