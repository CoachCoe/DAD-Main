# syntax=docker/dockerfile:1
FROM golang:1.27.1@sha256:e0174e51e81218523251d85d248a90d24c3d5e81543b4f07a5d66229397db190 AS build
WORKDIR /src
COPY apps/donor-api/go.mod apps/donor-api/go.sum ./
RUN go mod download
COPY apps/donor-api ./
RUN CGO_ENABLED=0 go build -trimpath -o /out/api ./cmd/api && \
    CGO_ENABLED=0 go build -trimpath -o /out/worker ./cmd/worker && \
    CGO_ENABLED=0 go build -trimpath -o /out/migrate ./cmd/migrate && \
    CGO_ENABLED=0 go build -trimpath -o /out/provision ./cmd/provision && \
    CGO_ENABLED=0 go build -trimpath -o /out/monitor ./cmd/monitor && \
    CGO_ENABLED=0 go build -trimpath -o /out/staff ./cmd/staff && \
    CGO_ENABLED=0 go build -trimpath -o /out/rekey ./cmd/rekey

FROM alpine:3.22@sha256:5291449c3df73caf6ed85e649dec1b9e818b39a5d8c871e97afc13e9cd5e8fa8 AS runtime
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 donor
COPY --from=build /out/ /usr/local/bin/
USER donor
ENV DONOR_GO_LISTEN=0.0.0.0:8002
EXPOSE 8002
CMD ["api"]
