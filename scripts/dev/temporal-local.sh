#!/usr/bin/env bash
set -euo pipefail

NAME="${TEMPORAL_DOCKER_NAME:-chum-temporal-local}"
IMAGE="${TEMPORAL_IMAGE:-temporalio/auto-setup:1.28.1}"
HOST_PORT="${TEMPORAL_PORT:-8233}"

usage() {
  cat <<USAGE
Usage: $(basename "$0") <start|stop|status|logs|env>

Commands:
  start   Start isolated Temporal container on 127.0.0.1:${HOST_PORT}
  stop    Stop and remove container '${NAME}'
  status  Show container status
  logs    Tail container logs
  env     Print env vars to point CHUM to this local Temporal

Environment overrides:
  TEMPORAL_DOCKER_NAME   Container name (default: ${NAME})
  TEMPORAL_IMAGE         Docker image (default: ${IMAGE})
  TEMPORAL_PORT          Host port mapped to container 7233 (default: ${HOST_PORT})
USAGE
}

cmd="${1:-status}"

require_docker() {
  if ! command -v docker >/dev/null 2>&1; then
    echo "docker binary not found in PATH"
    exit 1
  fi
  if ! docker info >/dev/null 2>&1; then
    echo "docker daemon is not reachable; start Docker Desktop/Engine first"
    exit 1
  fi
}

case "$cmd" in
  start)
    require_docker
    if docker ps -a --format '{{.Names}}' | grep -Fxq "$NAME"; then
      if docker ps --format '{{.Names}}' | grep -Fxq "$NAME"; then
        echo "Temporal container '${NAME}' already running"
      else
        docker start "$NAME" >/dev/null
        echo "Temporal container '${NAME}' started"
      fi
    else
      docker run -d \
        --name "$NAME" \
        -p "${HOST_PORT}:7233" \
        "$IMAGE" >/dev/null
      echo "Temporal container '${NAME}' created and started on 127.0.0.1:${HOST_PORT}"
    fi
    ;;
  stop)
    require_docker
    if docker ps -a --format '{{.Names}}' | grep -Fxq "$NAME"; then
      docker rm -f "$NAME" >/dev/null
      echo "Temporal container '${NAME}' removed"
    else
      echo "Temporal container '${NAME}' not found"
    fi
    ;;
  status)
    require_docker
    docker ps -a --filter "name=^/${NAME}$" --format 'table {{.Names}}\t{{.Status}}\t{{.Ports}}'
    ;;
  logs)
    require_docker
    docker logs -f "$NAME"
    ;;
  env)
    cat <<ENV
# CHUM reads Temporal host from [general].temporal_host_port in chum.toml.
# Use this value when creating a local config copy.
export CHUM_TEMPORAL_LOCAL_HOST_PORT=127.0.0.1:${HOST_PORT}
export TEMPORAL_NAMESPACE=default
ENV
    ;;
  *)
    usage
    exit 1
    ;;
esac
