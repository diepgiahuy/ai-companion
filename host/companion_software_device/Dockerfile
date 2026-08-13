FROM golang:1.26.5-bookworm

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential cmake ninja-build pkg-config curl ca-certificates python3 \
    libboost-system1.74-dev=1.74.0+ds1-21 \
    libopus-dev=1.3.1-3 \
    nlohmann-json3-dev=3.11.2-2 \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /src
COPY backend/go.mod backend/go.sum ./backend/
RUN cd backend && go mod download
COPY . .
RUN go env GOVERSION | grep -Eq '^go1\.26\.5([.]|$)'
RUN cd backend && go build -tags nolibopusfile -o /usr/local/bin/companiond ./cmd/companiond
RUN cmake -S host/companion_software_device -B /build/software-device -G Ninja -DCMAKE_BUILD_TYPE=Release \
 && cmake --build /build/software-device --target companion_software_device \
 && cp /build/software-device/companion_software_device /usr/local/bin/companion_software_device

CMD ["bash", "host/companion_software_device/run_e2e.sh"]
