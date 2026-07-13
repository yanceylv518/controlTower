FROM node:20-alpine AS web-builder

WORKDIR /src/webapp

COPY webapp/ ./
RUN corepack enable \
    && pnpm install --frozen-lockfile \
    && pnpm build

FROM golang:1.24-alpine AS server-builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY server/ ./server/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -trimpath \
    -ldflags "-s -w" \
    -o /out/control-tower-server \
    ./server/cmd/control-tower-server

FROM alpine:3.20

RUN addgroup -S -g 1001 controltower \
    && adduser -S -D -H -u 1001 -G controltower controltower

WORKDIR /app

COPY --from=server-builder --chown=1001:1001 /out/control-tower-server ./control-tower-server
COPY --from=server-builder --chown=1001:1001 /src/server/migrations/ ./server/migrations/
COPY --from=web-builder --chown=1001:1001 /src/web/dist/desktop/ ./web/dist/desktop/

USER 1001:1001

EXPOSE 8080

ENTRYPOINT ["/app/control-tower-server"]
