# syntax=docker/dockerfile:1

ARG GO_IMAGE=golang:1.26-alpine
FROM ${GO_IMAGE} AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
RUN CGO_ENABLED=0 go build \
      -trimpath \
      -ldflags "-s -w \
        -X github.com/moureau/menina/internal/version.Version=${VERSION} \
        -X github.com/moureau/menina/internal/version.Commit=${COMMIT} \
        -X github.com/moureau/menina/internal/version.Date=${DATE}" \
      -o /out/menina ./cmd/menina

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata curl \
 && adduser -D -H -u 10001 menina

COPY --from=build /out/menina /usr/local/bin/menina
COPY deploy /opt/menina/deploy

WORKDIR /home/menina
USER 10001:10001

ENTRYPOINT ["/usr/local/bin/menina"]
CMD ["serve"]
