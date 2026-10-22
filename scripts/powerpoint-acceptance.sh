#!/usr/bin/env bash
# powerpoint-acceptance.sh — open a compiler-built .pptx in real desktop
# PowerPoint and report what PowerPoint said.
#
# Two runtimes, chosen per profile, because the cheap one is not always
# available and neither is strictly better:
#
#   container  dockur/windows under podman or docker: Windows in QEMU/KVM
#              inside a container, which downloads its own Windows media.
#              Least setup; costs tens of GB of image and disk.
#   qemu       A VM image you built and provisioned yourself, snapshotted.
#              More setup; reproducible, and the snapshot makes two runs
#              comparable.
#
# What neither provides is **PowerPoint**. Windows alone cannot answer the
# question this harness exists for -- the defects that shipped were rejected
# by PowerPoint's OPC parser, and nothing else has one. Office must be
# installed into the image, which needs a licence and a licence is not
# something a script can arrange.
#
# Why a GUI runtime at all: PowerPoint automation needs a logged-in
# interactive desktop. A native Windows container has no interactive session,
# so a repair prompt has nowhere to appear and the COM call that raised it
# never returns -- the harness would hang rather than report.
#
# Exit codes are deliberately distinct, because "PowerPoint refused the file"
# and "we never got to ask PowerPoint" must never be confused:
#   0  PowerPoint opened the file and every assertion passed
#   1  the test ran and FAILED -- PowerPoint refused, or a dialog appeared
#   2  usage or profile error
#   3  infrastructure error: the PowerPoint test did NOT run
set -uo pipefail

PROG="$(basename "$0")"
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
HARNESS_DIR="$REPO_ROOT/acceptance/powerpoint"
PROFILE_DIR="$HARNESS_DIR/profiles"

EXIT_USAGE=2
EXIT_INFRA=3

PROFILE=""
PPTX=""
SLIDES=""
ARTIFACTS=""
RUN_TIMEOUT=900
BOOT_TIMEOUT=600
DRY_RUN=0
KEEP_VM=0

QEMU_PID=""
CONTAINER_STARTED=0
CLEANED=0

usage() {
  cat <<EOF
usage: $PROG --profile <name> --pptx <file> [options]

  --profile <name>   VM profile: $PROFILE_DIR/<name>.env
  --pptx <file>      the .pptx to test, built by this compiler on the host
  --slides <n>       slide count PowerPoint must report
  --artifacts <dir>  where diagnostics land (default: .bin/powerpoint/<profile>)
  --timeout <s>      hard limit on the guest test run (default: $RUN_TIMEOUT)
  --boot-timeout <s> hard limit on waiting for the guest (default: $BOOT_TIMEOUT)
  --dry-run          validate the profile and print the plan; start nothing
  --keep-vm          leave the guest running afterwards (debug)

Exit: 0 pass · 1 PowerPoint refused the file · 2 usage · 3 test did not run
EOF
}

say() { printf '%s: %s\n' "$PROG" "$*" >&2; }
die() {
  say "$*"
  exit "$EXIT_USAGE"
}
# Every infra failure routes through here so the "did NOT run" line is
# impossible to miss in a log, and impossible to mistake for a verdict.
infra() {
  say "$*"
  say "RESULT: the PowerPoint acceptance test DID NOT RUN. This is not a pass."
  exit "$EXIT_INFRA"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --profile)      PROFILE="${2:-}"; shift 2 || die "--profile needs a value" ;;
    --pptx)         PPTX="${2:-}"; shift 2 || die "--pptx needs a value" ;;
    --slides)       SLIDES="${2:-}"; shift 2 || die "--slides needs a value" ;;
    --artifacts)    ARTIFACTS="${2:-}"; shift 2 || die "--artifacts needs a value" ;;
    --timeout)      RUN_TIMEOUT="${2:-}"; shift 2 || die "--timeout needs a value" ;;
    --boot-timeout) BOOT_TIMEOUT="${2:-}"; shift 2 || die "--boot-timeout needs a value" ;;
    --dry-run)      DRY_RUN=1; shift ;;
    --keep-vm)      KEEP_VM=1; shift ;;
    -h|--help)      usage; exit 0 ;;
    *)              usage >&2; die "unknown argument: $1" ;;
  esac
done

[[ -n "$PROFILE" ]] || { usage >&2; die "--profile is required"; }
[[ -n "$PPTX" ]] || { usage >&2; die "--pptx is required"; }

PROFILE_FILE="$PROFILE_DIR/$PROFILE.env"
[[ -f "$PROFILE_FILE" ]] || die "no such profile: $PROFILE_FILE (see profiles/example-*.env)"
[[ -f "$PPTX" ]] || die "no such .pptx: $PPTX"

# shellcheck source=/dev/null
. "$PROFILE_FILE"

RUNTIME="${RUNTIME:-qemu}"
case "$RUNTIME" in
  container|qemu) ;;
  *) die "$PROFILE_FILE sets RUNTIME=$RUNTIME; want 'container' or 'qemu'" ;;
esac

# Shared by both runtimes: the guest is always reached over SSH, and the test
# is always triggered as a scheduled task in the interactive session.
for var in VM_SSH_PORT VM_SSH_USER VM_SSH_KEY GUEST_WORKDIR GUEST_TASK; do
  eval "value=\${$var:-}"
  [[ -n "$value" ]] || die "$PROFILE_FILE does not set $var"
done

if [[ "$RUNTIME" == qemu ]]; then
  for var in VM_DISK VM_SNAPSHOT; do
    eval "value=\${$var:-}"
    [[ -n "$value" ]] || die "$PROFILE_FILE sets RUNTIME=qemu but does not set $var"
  done
else
  for var in CONTAINER_IMAGE CONTAINER_NAME CONTAINER_STORAGE WINDOWS_VERSION; do
    eval "value=\${$var:-}"
    [[ -n "$value" ]] || die "$PROFILE_FILE sets RUNTIME=container but does not set $var"
  done
fi

VM_LABEL="${VM_LABEL:-$PROFILE}"
VM_MEMORY_MB="${VM_MEMORY_MB:-4096}"
VM_CPUS="${VM_CPUS:-2}"
CONTAINER_RUNTIME="${CONTAINER_RUNTIME:-podman}"
ARTIFACTS="${ARTIFACTS:-$REPO_ROOT/.bin/powerpoint/$PROFILE}"

# The snapshot is restored every run, so the guest's host key legitimately
# changes every run; pinning it would fail by design. The guest is reachable
# only on a loopback forward, never the network.
SSH_BASE_OPTS=(
  -o StrictHostKeyChecking=no
  -o UserKnownHostsFile=/dev/null
  -o LogLevel=ERROR
  -o ConnectTimeout=10
  -i "$VM_SSH_KEY"
)
# ssh takes the port as -p, scp as -P, and scp's -p means "preserve times".
# Two arrays rather than one patched at the call site: getting that wrong
# silently turns the port into a filename.
SSH_OPTS=("${SSH_BASE_OPTS[@]}" -p "$VM_SSH_PORT")
SCP_OPTS=("${SSH_BASE_OPTS[@]}" -P "$VM_SSH_PORT")
SSH_TARGET="$VM_SSH_USER@127.0.0.1"

# Commands are expanded on the client on purpose -- the paths come from the
# profile and are built here, not in the guest's shell.
# shellcheck disable=SC2029
guest_ssh() { ssh "${SSH_OPTS[@]}" "$SSH_TARGET" "$@"; }
guest_scp() { scp "${SCP_OPTS[@]}" "$@"; }

plan() {
  cat <<EOF
profile:    $PROFILE  ($VM_LABEL)
runtime:    $RUNTIME
EOF
  if [[ "$RUNTIME" == qemu ]]; then
    cat <<EOF
disk:       $VM_DISK
snapshot:   $VM_SNAPSHOT        (restored before and after)
EOF
  else
    cat <<EOF
image:      $CONTAINER_IMAGE  (via $CONTAINER_RUNTIME)
windows:    $WINDOWS_VERSION
storage:    $CONTAINER_STORAGE
container:  $CONTAINER_NAME
EOF
  fi
  cat <<EOF
resources:  ${VM_CPUS} cpu, ${VM_MEMORY_MB} MB
ssh:        $SSH_TARGET:$VM_SSH_PORT  key=$VM_SSH_KEY
guest dir:  $GUEST_WORKDIR
guest task: $GUEST_TASK
pptx:       $PPTX
slides:     ${SLIDES:-<not asserted>}
artifacts:  $ARTIFACTS
timeouts:   boot ${BOOT_TIMEOUT}s, run ${RUN_TIMEOUT}s
EOF
}

# ── runtimes ─────────────────────────────────────────────────────────────────

qemu_start() {
  command -v qemu-system-x86_64 >/dev/null 2>&1 || infra "qemu-system-x86_64 is not installed"
  command -v qemu-img >/dev/null 2>&1 || infra "qemu-img is not installed"
  [[ -f "$VM_DISK" ]] || infra "VM disk not found: $VM_DISK (provision it first; see README.md)"
  qemu-img snapshot -l "$VM_DISK" 2>/dev/null | grep -qw "$VM_SNAPSHOT" \
    || infra "snapshot '$VM_SNAPSHOT' not found on $VM_DISK"

  say "restoring snapshot $VM_SNAPSHOT"
  qemu-img snapshot -a "$VM_SNAPSHOT" "$VM_DISK" || infra "could not restore snapshot"

  say "booting the VM"
  qemu-system-x86_64 \
    -name "ppt-acceptance-$PROFILE" \
    -machine q35,accel=kvm \
    -cpu host \
    -smp "$VM_CPUS" \
    -m "$VM_MEMORY_MB" \
    -drive file="$VM_DISK",if=virtio,cache=writeback \
    -netdev user,id=n0,hostfwd=tcp:127.0.0.1:"$VM_SSH_PORT"-:22 \
    -device virtio-net-pci,netdev=n0 \
    -display none \
    -daemonize \
    -pidfile "$ARTIFACTS/qemu.pid" \
    >"$ARTIFACTS/qemu.log" 2>&1 \
    || infra "qemu failed to start (see $ARTIFACTS/qemu.log)"

  QEMU_PID="$(cat "$ARTIFACTS/qemu.pid" 2>/dev/null || true)"
  [[ -n "$QEMU_PID" ]] || infra "qemu started but wrote no pidfile"
}

qemu_alive() { [[ -n "$QEMU_PID" ]] && kill -0 "$QEMU_PID" 2>/dev/null; }

# shellcheck disable=SC2317  # reached through the EXIT trap
qemu_stop() {
  if qemu_alive; then
    guest_ssh "shutdown /s /t 0" >/dev/null 2>&1 || true
    for _ in $(seq 1 30); do
      qemu_alive || break
      sleep 1
    done
    if qemu_alive; then
      say "VM did not shut down; terminating qemu"
      kill -TERM "$QEMU_PID" 2>/dev/null || true
      sleep 3
      kill -KILL "$QEMU_PID" 2>/dev/null || true
    fi
  fi
  # Always leave the disk as it was found, even after a failure -- otherwise
  # the next run inherits Office's crash state and measures something else.
  if [[ -n "${VM_DISK:-}" && -f "${VM_DISK:-}" ]]; then
    qemu-img snapshot -a "$VM_SNAPSHOT" "$VM_DISK" 2>/dev/null \
      || say "WARNING: could not restore snapshot $VM_SNAPSHOT -- the disk is dirty"
  fi
}

container_start() {
  command -v "$CONTAINER_RUNTIME" >/dev/null 2>&1 \
    || infra "$CONTAINER_RUNTIME is not installed"
  [[ -e /dev/kvm ]] || infra "/dev/kvm is absent, so the guest would fall back to software emulation"
  [[ -r /dev/kvm && -w /dev/kvm ]] \
    || infra "/dev/kvm is not readable and writable by this user (add it to the kvm group)"

  # The Windows disk lives where the profile says, not in the container
  # runtime's own store. That store is frequently on a cache tier with little
  # headroom, and a Windows install is tens of GB.
  mkdir -p "$CONTAINER_STORAGE" || infra "cannot create $CONTAINER_STORAGE"

  # A container left from an interrupted run owns the port and the storage.
  "$CONTAINER_RUNTIME" rm -f "$CONTAINER_NAME" >/dev/null 2>&1 || true

  say "starting $CONTAINER_IMAGE ($WINDOWS_VERSION) as $CONTAINER_NAME"
  "$CONTAINER_RUNTIME" run -d \
    --name "$CONTAINER_NAME" \
    --device=/dev/kvm \
    --cap-add NET_ADMIN \
    -e "VERSION=$WINDOWS_VERSION" \
    -e "RAM_SIZE=${VM_MEMORY_MB}M" \
    -e "CPU_CORES=$VM_CPUS" \
    -p "127.0.0.1:$VM_SSH_PORT:22" \
    -v "$CONTAINER_STORAGE:/storage" \
    "$CONTAINER_IMAGE" \
    >"$ARTIFACTS/container.log" 2>&1 \
    || infra "could not start the container (see $ARTIFACTS/container.log)"
  CONTAINER_STARTED=1
}

container_alive() {
  [[ "$CONTAINER_STARTED" -eq 1 ]] \
    && "$CONTAINER_RUNTIME" inspect -f '{{.State.Running}}' "$CONTAINER_NAME" 2>/dev/null | grep -q true
}

# shellcheck disable=SC2317  # reached through the EXIT trap
container_stop() {
  [[ "$CONTAINER_STARTED" -eq 1 ]] || return 0
  say "stopping $CONTAINER_NAME"
  "$CONTAINER_RUNTIME" logs "$CONTAINER_NAME" >"$ARTIFACTS/container.log" 2>&1 || true
  "$CONTAINER_RUNTIME" rm -f "$CONTAINER_NAME" >/dev/null 2>&1 || true
}

# Dispatch with if/else, not `A && B || C`: that form runs C whenever B is
# false, so `runtime_alive` would have asked the container whether it was
# running every time the VM was found dead -- and answered "no runtime
# problem" on the wrong runtime.
runtime_start() {
  if [[ "$RUNTIME" == qemu ]]; then qemu_start; else container_start; fi
}

runtime_alive() {
  if [[ "$RUNTIME" == qemu ]]; then qemu_alive; else container_alive; fi
}

# Reached through the EXIT trap as well as directly.
# shellcheck disable=SC2317
runtime_stop() {
  if [[ "$RUNTIME" == qemu ]]; then qemu_stop; else container_stop; fi
}

# Invoked only through the EXIT/INT/TERM trap below.
# shellcheck disable=SC2317
cleanup() {
  [[ "$CLEANED" -eq 1 ]] && return
  CLEANED=1
  if [[ "$KEEP_VM" -eq 1 ]]; then
    say "--keep-vm: leaving the guest running"
    return
  fi
  runtime_stop
}
trap cleanup EXIT INT TERM

if [[ "$DRY_RUN" -eq 1 ]]; then
  plan
  say "--dry-run: validated the profile and the .pptx; nothing was started"
  CLEANED=1
  exit 0
fi

for tool in ssh scp; do
  command -v "$tool" >/dev/null 2>&1 || infra "$tool is not installed"
done
[[ -r "$VM_SSH_KEY" ]] || infra "cannot read SSH key: $VM_SSH_KEY"

mkdir -p "$ARTIFACTS" || infra "cannot create $ARTIFACTS"
rm -rf "${ARTIFACTS:?}/guest"
plan | tee "$ARTIFACTS/plan.txt" >&2

runtime_start

say "waiting up to ${BOOT_TIMEOUT}s for the guest"
deadline=$(( $(date +%s) + BOOT_TIMEOUT ))
until guest_ssh "echo ready" >/dev/null 2>&1; do
  [[ "$(date +%s)" -lt "$deadline" ]] \
    || infra "the guest never became reachable over SSH within ${BOOT_TIMEOUT}s"
  runtime_alive || infra "the guest exited while booting (see $ARTIFACTS)"
  sleep 5
done
say "guest is up"

# The guest must be at a logged-on interactive desktop, not just booted:
# PowerPoint automation in session 0 fails in ways that look like corruption.
if ! guest_ssh "query session" 2>/dev/null | grep -qiE 'console .*active'; then
  infra "no active console session in the guest -- autologon is required (see README.md)"
fi

say "copying the harness and the artifact into the guest"
guest_ssh "if not exist \"$GUEST_WORKDIR\" mkdir \"$GUEST_WORKDIR\"" >/dev/null 2>&1 || true
guest_ssh "if exist \"$GUEST_WORKDIR\\artifacts\" rmdir /s /q \"$GUEST_WORKDIR\\artifacts\"" >/dev/null 2>&1 || true
guest_scp -r "$HARNESS_DIR"/*.py "$HARNESS_DIR/requirements.txt" "$HARNESS_DIR/run-tests.cmd" \
  "$SSH_TARGET:$GUEST_WORKDIR/" \
  || infra "could not copy the harness into the guest"
guest_scp "$PPTX" "$SSH_TARGET:$GUEST_WORKDIR/under-test.pptx" \
  || infra "could not copy the .pptx into the guest"

# The task reads its parameters from a file rather than taking arguments:
# schtasks /Run cannot pass any, and baking them into the task definition
# would mean re-registering it per run.
runfile="$ARTIFACTS/run.env"
{
  echo "POWERPOINT_ACCEPTANCE_PPTX=$GUEST_WORKDIR/under-test.pptx"
  echo "POWERPOINT_ACCEPTANCE_ARTIFACTS=$GUEST_WORKDIR/artifacts"
  echo "POWERPOINT_ACCEPTANCE_REQUIRED=1"
  [[ -n "$SLIDES" ]] && echo "POWERPOINT_ACCEPTANCE_SLIDES=$SLIDES"
} >"$runfile"
guest_scp "$runfile" "$SSH_TARGET:$GUEST_WORKDIR/run.env" \
  || infra "could not stage the run parameters"

say "triggering $GUEST_TASK in the interactive session"
guest_ssh "schtasks /Run /TN \"$GUEST_TASK\"" >/dev/null 2>&1 \
  || infra "could not start the scheduled task '$GUEST_TASK' (registered? see README.md)"

say "waiting up to ${RUN_TIMEOUT}s for the guest test to finish"
deadline=$(( $(date +%s) + RUN_TIMEOUT ))
guest_status=""
while :; do
  guest_status="$(guest_ssh "type \"$GUEST_WORKDIR\\artifacts\\exitcode\"" 2>/dev/null | tr -d '\r\n ')"
  case "$guest_status" in
    ''|*[!0-9]*) : ;;
    *) break ;;
  esac
  if [[ "$(date +%s)" -ge "$deadline" ]]; then
    say "guest test exceeded ${RUN_TIMEOUT}s"
    guest_scp -r "$SSH_TARGET:$GUEST_WORKDIR/artifacts" "$ARTIFACTS/guest" >/dev/null 2>&1 || true
    infra "the guest test did not finish; partial diagnostics in $ARTIFACTS/guest"
  fi
  runtime_alive || infra "the guest died mid-test (see $ARTIFACTS)"
  sleep 5
done

say "collecting diagnostics"
guest_scp -r "$SSH_TARGET:$GUEST_WORKDIR/artifacts" "$ARTIFACTS/guest" >/dev/null 2>&1 \
  || say "WARNING: could not copy the guest's artifacts back"

if [[ "$guest_status" -eq 0 ]]; then
  say "RESULT: PowerPoint ($VM_LABEL) opened the file and every assertion passed"
else
  say "RESULT: the PowerPoint test RAN and FAILED (guest exit $guest_status)"
  say "        read $ARTIFACTS/guest for the dialog text, screenshots and slide exports"
fi
exit "$guest_status"
