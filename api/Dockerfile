# Build context is the repository root: the API imports runner/proto.
FROM golang:1.26 AS build
WORKDIR /src
COPY runner/ runner/
COPY api/ api/
RUN cd api && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api .

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/api /api
ENTRYPOINT ["/api"]
