set dotenv-load := false

image := env_var_or_default("IMAGE", "trace-sync-server:dev")
port := env_var_or_default("PORT", "8787")
data_dir := env_var_or_default("DATA_DIR", "./data")
token := env_var_or_default("TRACE_SYNC_TOKEN", "dev-token-change-me")

default:
    @just --list

fmt:
    gofmt -w main.go main_test.go

test:
    go test ./...

check: fmt test

build:
    go build -o trace-sync-server .

run:
    TRACE_SYNC_TOKEN="{{token}}" TRACE_SYNC_DATA_DIR="{{data_dir}}" TRACE_SYNC_PORT="{{port}}" go run .

docker-build:
    docker build -t "{{image}}" .

docker-run: docker-build
    mkdir -p "{{data_dir}}"
    data_path="$(realpath "{{data_dir}}")"; docker run --rm --user "$(id -u):$(id -g)" -p "{{port}}:8787" -v "$data_path:/data" -e TRACE_SYNC_TOKEN="{{token}}" "{{image}}"

e2e:
    ./e2e.sh

clean:
    rm -f trace-sync-server
    rm -rf data
