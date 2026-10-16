FROM golang:1.25.3-bookworm AS build
WORKDIR /src
ENV CGO_ENABLED=0
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY *.go ./
COPY web/ ./web/
RUN go test ./... && go build -buildvcs=false -trimpath -ldflags="-s -w" -o /out/walter .
RUN mkdir -m 0700 /data

FROM scratch
ARG VERSION=dev
ARG REVISION=unknown
LABEL org.opencontainers.image.title="Walter" \
      org.opencontainers.image.description="Minimal private chat with llama.cpp and OIDC" \
      org.opencontainers.image.source="https://github.com/mitschwimmer/walter" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}"
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/walter /walter
COPY --from=build --chown=65532:65532 /data /data
USER 65532:65532
ENV WALTER_LISTEN=0.0.0.0:8080 WALTER_DATA_DIR=/data
EXPOSE 8080
STOPSIGNAL SIGTERM
ENTRYPOINT ["/walter"]
