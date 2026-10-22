# Small image: the one program plus the map data, no operating system inside.
# The build workflow puts us-parks.geojson.gz, sota.geojson.gz and nfer.geojson.gz in data/ first.
FROM --platform=$BUILDPLATFORM golang:1.24 AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd cmd
COPY internal internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/park-map ./cmd/parkmap

FROM scratch
COPY --from=build /out/park-map /park-map
# Root certificates, so the satellite gateway can reach POTA, SOTA, Garmin and mail servers.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY data/us-parks.geojson.gz data/sota.geojson.gz data/nfer.geojson.gz /data/
ENV PARK_MAP_DATA=/data PARK_MAP_CONFIG=/config PARK_MAP_LISTEN=0.0.0.0:8075
# Gateway settings (including passwords) and its log live here, outside the image.
VOLUME /config
# Unraid reads these to show the icon and an "open the page" link however the container was added.
LABEL net.unraid.docker.icon="https://raw.githubusercontent.com/jsttylr/park-map/main/unraid/icon.png" \
      net.unraid.docker.webui="http://[IP]:[PORT:8075]/" \
      org.opencontainers.image.source="https://github.com/jsttylr/park-map"
EXPOSE 8075/tcp
# Working out the overlaps takes a minute or two at startup.
HEALTHCHECK --interval=30s --timeout=5s --start-period=5m CMD ["/park-map", "-healthcheck"]
ENTRYPOINT ["/park-map"]
