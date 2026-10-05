# syntax=docker/dockerfile:1
FROM golang:1.27.1 AS build
WORKDIR /src
COPY apps/donor-api/go.mod apps/donor-api/go.sum ./
RUN go mod download
COPY apps/donor-api ./
RUN CGO_ENABLED=0 go build -trimpath -o /out/api ./cmd/api && \
    CGO_ENABLED=0 go build -trimpath -o /out/worker ./cmd/worker && \
    CGO_ENABLED=0 go build -trimpath -o /out/migrate ./cmd/migrate && \
    CGO_ENABLED=0 go build -trimpath -o /out/monitor ./cmd/monitor && \
    CGO_ENABLED=0 go build -trimpath -o /out/staff ./cmd/staff

FROM alpine:3.22 AS runtime
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 donor
COPY --from=build /out/ /usr/local/bin/
USER donor
ENV DONOR_GO_LISTEN=0.0.0.0:8002
EXPOSE 8002
CMD ["api"]
