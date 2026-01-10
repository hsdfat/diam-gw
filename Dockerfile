# Multi-stage Dockerfile for production-grade EIR service

# Stage 1: Build
FROM hsdfat/ubi8-go:1.25.2 AS builder
ENV WORKDIR=/app
# Set working directory
WORKDIR ${WORKDIR}

RUN \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/root/go/pkg/mod \
    --mount=type=bind,target=$WORKDIR \
    go build -o /tmp/main ./cmd/gateway

# Stage 2: Runtime
FROM redhat/ubi8:latest

ENV WORKDIR=/app \
    USERNAME=gateway \
    GROUPNAME=gateway \
    USERID=2026 \
    GROUPID=2026 
#LD_LIBRARY_PATH=${LD_LIBRARY_PATH}:/usr/lib64

# Set working directory
WORKDIR ${WORKDIR}

# Copy binary from builder
COPY --from=builder /tmp/main /app/main

# Change ownership
RUN groupadd --gid ${GROUPID} ${GROUPNAME} && \
    useradd --uid ${USERID} --gid ${GROUPID} -m ${USERNAME} && \
    chown -R ${USERNAME}:${GROUPNAME} /app

# Switch to non-root user
USER ${USERNAME}

# Run the application
ENTRYPOINT ["/app/main"]
