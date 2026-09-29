FROM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/manifest-repo ./cmd/manifest-repo

FROM alpine:3.22

RUN apk add --no-cache ca-certificates docker-cli-compose git openssh-client

COPY --from=build /out/manifest-repo /usr/local/bin/manifest-repo

WORKDIR /github/workspace

ENTRYPOINT ["/usr/local/bin/manifest-repo"]
