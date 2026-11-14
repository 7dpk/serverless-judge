# Builds the two binaries every language image copies in:
#   /judge  the runner server and in-sandbox supervisor (static Go)
#   /jexec  the launcher that applies limits and reports usage (static C)
FROM golang:1.26 AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/judge ./cmd/judge \
 && gcc -O2 -static -o /out/jexec jexec/jexec.c

FROM scratch
COPY --from=build /out/judge /out/jexec /
