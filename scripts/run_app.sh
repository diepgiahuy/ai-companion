#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

echo "========================================================"
echo "          AI COMPANION PLATFORM - START SCRIPT          "
echo "========================================================"

command -v docker >/dev/null 2>&1 || {
  echo "❌ Error: Docker is required to run the AI Companion stack." >&2
  exit 1
}

echo "📦 1. Starting PostgreSQL, Migrations & Runtime Stack via Docker Compose..."
docker compose up -d

echo "⏳ 2. Waiting for Authoritative Services & Healthcheck..."
for i in {1..30}; do
  if curl -s -f "http://127.0.0.1:8000/v1/owner/dashboard" >/dev/null 2>&1; then
    echo "✅ Backend Daemon & Owner Web are LIVE!"
    break
  fi
  sleep 1
done

echo ""
echo "========================================================"
echo "🎉 AI Companion Stack is Ready & Grounded in PostgreSQL!"
echo "========================================================"
echo ""
echo "🌐 Owner Web Workspace: http://localhost:8000/v1/owner/dashboard"
echo "📡 Device Protocol Uplink: ws://localhost:8000/v1/ws"
echo "🗄️ PostgreSQL Database:   127.0.0.1:55432 (user: companion_app)"
echo ""
echo "To view live logs:    docker compose logs -f backend"
echo "To stop the stack:    docker compose down"
echo "========================================================"
