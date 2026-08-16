#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

# ── Pre-flight checks ────────────────────────────────────
command -v docker >/dev/null 2>&1 || { echo "❌ Docker is not installed." >&2; exit 1; }

if [ ! -f .env ]; then
  echo "❌ Missing .env file. Copy the template and fill in your API keys:"
  echo ""
  echo "   cp .env.example .env"
  echo "   \$EDITOR .env"
  echo ""
  exit 1
fi

# ── Launch ────────────────────────────────────────────────
echo "🚀 Starting AI Companion stack..."
docker compose up -d --build

echo ""
echo "✅ Stack launched. Use 'docker compose logs -f backend' to watch startup."
echo ""
echo "   🌐 Dashboard:  http://localhost:8000/v1/owner/dashboard"
echo "   📡 WebSocket:  ws://localhost:8000/v1/ws"
echo "   🗄️  Postgres:   127.0.0.1:55432"
echo ""
echo "   Stop:  docker compose down"
