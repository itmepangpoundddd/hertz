# syntax=docker/dockerfile:1

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=unknown
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags "-s -w -X hertz/internal/version.Version=${VERSION} -X hertz/internal/version.Commit=${COMMIT}" \
    -o /out/hertz ./cmd/hertz

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/hertz /hertz
EXPOSE 8080/tcp 9090/tcp 8443/udp 5004/udp
USER nonroot:nonroot
ENTRYPOINT ["/hertz"]
