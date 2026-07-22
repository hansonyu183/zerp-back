FROM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/zerp-server ./cmd/server

FROM alpine:3.23

RUN apk add --no-cache ca-certificates \
    && addgroup -S zerp \
    && adduser -S -G zerp zerp

COPY --from=build /out/zerp-server /usr/local/bin/zerp-server

USER zerp
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/zerp-server"]
