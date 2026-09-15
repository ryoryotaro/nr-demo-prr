FROM golang:1.25-alpine AS build

WORKDIR /src
COPY go.mod ./
COPY go.sum ./
RUN go mod download
COPY cmd ./cmd

ARG SERVICE
RUN --mount=type=cache,target=/root/.cache/go-build \
    GOMAXPROCS=1 GOGC=50 CGO_ENABLED=0 go build -p 1 -o /service ./cmd/${SERVICE}

FROM alpine:3.20
RUN adduser -D -u 10001 appuser
USER appuser
COPY --from=build /service /service
ENTRYPOINT ["/service"]
