# Distributed API image: one static Go binary serving the JSON API + the web
# UI (embedded via go:embed from web/dist, built in the node stage below).

FROM node:22-alpine AS webbuilder

WORKDIR /src/web

RUN npm install -g pnpm@10
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile

COPY web/ ./
# The distributed brain serves the distributed-flavor UI; base URLs stay empty
# (same-origin, see web/.env.production).
RUN VITE_APP_MODE=distributed pnpm run build

FROM golang:1.24 AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=webbuilder /src/web/dist ./web/dist

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o /out/api ./cmd/api

FROM gcr.io/distroless/base-debian12:nonroot

WORKDIR /app

COPY --from=builder /out/api ./api

ENV API_PORT=8080

EXPOSE 8080

ENTRYPOINT ["/app/api"]
