FROM golang:1.26.5-bookworm
RUN apt-get update \
 && apt-get install -y --no-install-recommends build-essential cmake ninja-build libopus-dev sqlite3 ca-certificates git python3 \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /workspace
