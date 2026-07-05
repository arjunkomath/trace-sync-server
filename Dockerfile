FROM golang:1.22-alpine AS build

WORKDIR /src
COPY go.mod ./
COPY main.go ./
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /trace-sync-server .

FROM scratch
COPY --from=build /trace-sync-server /trace-sync-server
EXPOSE 8787
VOLUME ["/data"]
ENV TRACE_SYNC_DATA_DIR=/data
ENTRYPOINT ["/trace-sync-server"]
