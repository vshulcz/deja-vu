# Build deja from source; the MCP server speaks stdio, so the container
# needs nothing beyond the binary.
FROM golang:1.27-alpine@sha256:fa3d596898d9f126af7c01d14ee7597bab3f39cccf392ef36b69fe77cd681f18 AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/deja ./cmd/deja

FROM alpine:3.24@sha256:3f4d6b32e26703c6082839234f43769d5046b3ac7c910033911d1e71d8cd630a
RUN apk add --no-cache sqlite
COPY --from=build /out/deja /usr/local/bin/deja
ENTRYPOINT ["deja"]
CMD ["mcp"]
