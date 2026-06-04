#!/usr/bin/env bash
# ============================================================
# 文心健康管家 Demo 一键启动脚本
# ------------------------------------------------------------
# 使用：
#   1. 首次：cp .env.example .env  然后编辑 .env 填入 API Key
#   2. 之后：./run.sh
# ============================================================

set -e

cd "$(dirname "$0")"

# 1. 检查 .env 是否存在
if [ ! -f .env ]; then
  echo "❌ 没有找到 .env 文件"
  echo "请先执行: cp .env.example .env  然后编辑 .env 填入你的 API Key"
  exit 1
fi

# 2. 加载 .env（自动 export 所有变量）
set -a
# shellcheck disable=SC1091
source .env
set +a

# 3. 检查必填项
if [ -z "${OPENAI_API_KEY:-}" ] || [ "${OPENAI_API_KEY}" = "sk-your-api-key-here" ]; then
  echo "❌ 请在 .env 中设置真实的 OPENAI_API_KEY"
  exit 1
fi

# 4. 检查 Go 是否安装
if ! command -v go >/dev/null 2>&1; then
  echo "❌ 没有检测到 Go。请先安装：brew install go"
  exit 1
fi

echo "✅ 启动服务... 浏览器打开 http://localhost:${PORT:-8080}"
echo

# 5. 启动 chatwitheino server
cd eino-examples/quickstart/chatwitheino
exec go run .
