#!/usr/bin/env bash

# Runs the end-to-end tests in a Lima VM.
# Arguments go to the test binary, for example: e2e/run.sh -test.run 'TestRun/in_srt'

set -euo pipefail

vm=aeg-e2e
root="$(cd "$(dirname "$0")/.." && pwd)"
bin="$root/e2e/bin"

log() { printf '==> %s\n' "$*"; }

if ! limactl list --quiet | grep -qx "$vm"; then
  log "Create the VM $vm. The first run downloads an image and installs srt, which takes some minutes."
  limactl create --tty=false --name "$vm" "$root/e2e/lima.yaml"
fi

if [ "$(limactl list --format '{{.Status}}' "$vm")" != Running ]; then
  log "Start the VM $vm"
  limactl start --tty=false "$vm"
fi

case "$(limactl list --format '{{.Arch}}' "$vm")" in
  aarch64) goarch=arm64 ;;
  x86_64) goarch=amd64 ;;
esac

log "Build AEG and the test binary for linux/$goarch"
mkdir -p "$bin"
GOOS=linux GOARCH=$goarch CGO_ENABLED=0 go build -o "$bin/aeg" "$root/cmd/aeg"
GOOS=linux GOARCH=$goarch CGO_ENABLED=0 go test -c -tags e2e -o "$bin/e2e.test" "$root/e2e"

log "Run the tests in the VM"
limactl shell --workdir /tmp "$vm" env AEG_BIN="$bin/aeg" "$bin/e2e.test" -test.v "$@"
