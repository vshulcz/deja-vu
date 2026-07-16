# Build stage
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/deja ./cmd/deja

# Runtime: sqlite3 is only needed when an opencode store is mounted in
FROM alpine:3.20
RUN apk add --no-cache sqlite
COPY --from=build /out/deja /usr/local/bin/deja
ENTRYPOINT ["deja"]
CMD ["mcp"]
