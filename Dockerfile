# syntax=docker/dockerfile:1

# ---- build: static binary, toolchain stays out of the final image ----
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker

# ---- runtime: only ffmpeg + the binary, non-root ----
FROM alpine:3.22
RUN apk add --no-cache ffmpeg ca-certificates \
 && addgroup -S -g 10001 app \
 && adduser -S -D -H -u 10001 -G app app \
 && mkdir /work && chown app:app /work
COPY --from=build /out/worker /usr/local/bin/worker
USER 10001:10001
ENV WORK_DIR=/work
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/worker"]
