#!/bin/bash
set -e
cd /home/ubuntu/projects/cortex
export PATH="/home/ubuntu/.local/bin:$PATH"
export GOROOT="/home/ubuntu/.local/go"
export GOPATH="/home/ubuntu/go"
export HOME="/home/ubuntu"
echo "Building chum from $(git rev-parse --short HEAD)..."
go build -o /home/ubuntu/projects/cortex/chum ./cmd/chum
echo "Build complete: $(stat -c '%Y %s' /home/ubuntu/projects/cortex/chum)"
