# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS build

WORKDIR /src

# Abhängigkeiten zuerst, damit die Ebene bei reinen Codeänderungen bestehen bleibt.
COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ ./cmd/
COPY internal/ ./internal/

# Statisch gebunden, damit das Ergebnis in einem leeren Abbild läuft.
# Zeitzonendaten sind über time/tzdata einkompiliert.
ARG VERSION=dev
RUN CGO_ENABLED=0 go build \
      -trimpath \
      -ldflags="-s -w -X main.version=${VERSION}" \
      -o /dockerapi \
      ./cmd/dockerapi

FROM scratch

LABEL org.opencontainers.image.title="mailcow-dockerapi"
LABEL org.opencontainers.image.description="Docker-API-Vermittler für mailcow"
LABEL org.opencontainers.image.source="https://bodsch.me/mailcow-dockerapi"

COPY --from=build /dockerapi /dockerapi

# Das Serverzertifikat erzeugt der Dienst beim Start selbst; openssl und das
# Entrypoint-Skript der Python-Fassung entfallen damit.
EXPOSE 443

ENTRYPOINT ["/dockerapi"]
