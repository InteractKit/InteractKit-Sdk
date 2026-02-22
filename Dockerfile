# ── Stage 1: Next.js static export ───────────────────────────────────────────
FROM node:20-slim AS ui-builder

WORKDIR /app/ui
COPY ui/package.json ui/package-lock.json* ./
RUN npm ci
COPY ui/ .
RUN npm run build

# ── Stage 2: Go build ───────────────────────────────────────────────────────
FROM golang:1.24-bookworm AS builder

ARG ONNXRUNTIME_VERSION=1.23.2
ARG SILERO_VAD_VERSION=5.1.2

RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc g++ \
    libsamplerate0-dev \
    libopus-dev libopusfile-dev libsoxr-dev \
    curl \
    git autoconf automake libtool \
    && rm -rf /var/lib/apt/lists/*

# Build rnnoise from source
RUN git clone --depth 1 https://github.com/xiph/rnnoise.git /tmp/rnnoise && \
    cd /tmp/rnnoise && \
    ./autogen.sh && \
    ./configure --prefix=/usr/local && \
    make -j$(nproc) && \
    make install && \
    ldconfig && \
    rm -rf /tmp/rnnoise

# Download ONNX Runtime from official GitHub releases
RUN mkdir -p /opt/onnx && \
    curl -fSL "https://github.com/microsoft/onnxruntime/releases/download/v${ONNXRUNTIME_VERSION}/onnxruntime-linux-x64-${ONNXRUNTIME_VERSION}.tgz" \
    | tar xz --strip-components=1 -C /opt/onnx

# Download dok
RUN mkdir -p /opt/models && \
    curl -fSL -o /opt/models/silero_vad.onnx \
    "https://github.com/snakers4/silero-vad/raw/v${SILERO_VAD_VERSION}/src/silero_vad/data/silero_vad.onnx"

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=1 go build -o /app/interactkit .
RUN CGO_ENABLED=0 go build -o /app/ui-server ./ui/server/

# ── Stage 3: Runtime ────────────────────────────────────────────────────────
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    libsamplerate0 \
    libopus0 libopusfile0 libsoxr0 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Binaries
COPY --from=builder /app/interactkit .
COPY --from=builder /app/ui-server .

# UI static files (Next.js static export)
COPY --from=ui-builder /app/ui/out/ ./ui/public/

# rnnoise shared library
COPY --from=builder /usr/local/lib/librnnoise*.so* /usr/local/lib/
RUN ldconfig

# ONNX Runtime shared libraries
COPY --from=builder /opt/onnx/lib/ ./external/onnx/

# Silero VAD model
COPY --from=builder /opt/models/ ./external/models/

ENV LD_LIBRARY_PATH=/app/external/onnx
ENV SILERO_MODEL_PATH=./external/models/silero_vad.onnx
ENV ONNX_RUNTIME_PATH=./external/onnx/libonnxruntime.so

EXPOSE 8888

ENTRYPOINT ["./ui-server"]
