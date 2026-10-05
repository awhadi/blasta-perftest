# syntax=docker/dockerfile:1

# ---- build ----
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/blasta ./cmd/blasta

# ---- runtime ----
FROM alpine:3.22
# ca-certificates so HTTPS/WSS targets verify; no other tooling is needed.
RUN apk add --no-cache ca-certificates \
 && adduser -D -u 10001 blasta \
 && mkdir /data && chown 10001:10001 /data
COPY --from=build /out/blasta /usr/local/bin/blasta
USER 10001
# Finished runs are saved here, so mount a volume to keep history across restarts.
ENV BLASTA_DATA_DIR=/data
VOLUME /data
EXPOSE 8080

# Inside the container blasta must listen on all interfaces, which it only does
# with --allow-remote. Keep the *published* port on loopback (see
# docker-compose.yml) so the host still exposes it to this machine only.
ENTRYPOINT ["blasta"]
CMD ["serve", "--addr", "0.0.0.0:8080", "--allow-remote"]

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/api/health >/dev/null || exit 1
