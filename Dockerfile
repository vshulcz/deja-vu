# Build stage
FROM golang:1.25-alpine@sha256:56961d79ea8129efddcc0b8643fd8a5416b4e6228cfd477e3fd61deb2672c587 AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/deja ./cmd/deja

# Runtime: sqlite3 is only needed when an opencode store is mounted in
FROM alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc
RUN apk add --no-cache sqlite
COPY --from=build /out/deja /usr/local/bin/deja
ENTRYPOINT ["deja"]
CMD ["mcp"]
