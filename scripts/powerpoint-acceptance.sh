#!/usr/bin/env bash
# powerpoint-acceptance.sh — open a compiler-built .pptx in real desktop
# PowerPoint inside a disposable Windows VM, and report what PowerPoint said.
#
# Why a VM and not a container: PowerPoint automation needs a logged-in
# interactive desktop. Windows containers have no interactive session, so a
# repair prompt has nowhere to appear and the COM call that raised it never
# returns -- the harness would hang rather than report. Microsoft's own
# guidance is a VM for GUI applications.
#
# Why a snapshot per run: Office is stateful. A previous crash leaves recovery
# documents, "safe mode" offers and a Document Recovery pane, any one of which
# changes what the next open does. Restoring a clean snapshot before AND after
# every run is what makes two runs comparable.
#
# What this script does NOT do: install Windows, Office, Python or the
# scheduled task. Those are one-time provisioning, documented in
# acceptance/powerpoint/README.md, and the images stay out of git.
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
BOOT_TIMEOUT=300
DRY_RUN=0
KEEP_VM=0

QEMU_PID=""
CLEANED=0

usage() {
  cat <<EOF
usage: $PROG --profile <name> --pptx <file> [options]

  --profile <name>   VM profile: $PROFILE_DIR/<name>.env
  --pptx <file>      the .pptx to test, built by this compiler on the host
  --slides <n>       slide count PowerPoint must report
  --artifacts <dir>  where diagnostics land (default: .bin/powerpoint/<profile>)
  --timeout <s>      hard limit on the guest test run (default: $RUN_TIMEOUT)
  --boot-timeout <s> hard limit on waiting for guest SSH (default: $BOOT_TIMEOUT)
  --dry-run          validate the profile and print the plan; boot nothing
  --keep-vm          leave the VM running and the snapshot unrestored (debug)

Exit: 0 pass · 1 PowerPoint refused the file · 2 usage · 3 test did not run
EOF
}

say()  { printf '%s: %s\n' "$PROG" "$*" >&2; }
die()  { say "$*"; exit "$EXIT_USAGE"; }
# Every infra failure routes through here so the "did NOT run" line is
# impossible to miss in a log, and impossible to mistake for a verdict.
infra() {
  say "$*"
  say "RESULT: the PowerPoint acceptance test DID NOT RUN. This is not a pass."
  exit "$EXIT_INFRA"
}

while [ $# -gt 0 ]; do
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

[ -n "$PROFILE" ] || { usage >&2; die "--profile is required"; }
[ -n "$PPTX" ] || { usage >&2; die "--pptx is required"; }

PROFILE_FILE="$PROFILE_DIR/$PROFILE.env"
[ -f "$PROFILE_FILE" ] || die "no such profile: $PROFILE_FILE (see profiles/example.env)"
[ -f "$PPTX" ] || die "no such .pptx: $PPTX"

# shellcheck source=/dev/null
. "$PROFILE_FILE"

for var in VM_DISK VM_SNAPSHOT VM_SSH_PORT VM_SSH_USER VM_SSH_KEY GUEST_WORKDIR GUEST_TASK; do
  eval "value=\${$var:-}"
  [ -n "$value" ] || die "$PROFILE_FILE does not set $var"
done
VM_LABEL="${VM_LABEL:-$PROFILE}"
VM_MEMORY_MB="${VM_MEMORY_MB:-4096}"
VM_CPUS="${VM_CPUS:-2}"

ARTIFACTS="${ARTIFACTS:-$REPO_ROOT/.bin/powerpoint/$PROFILE}"

# The snapshot is restored every run, so the guest's host key legitimately
# changes every run; pinning it would fail by design. The VM is reachable only
# on a loopback forward, never the network.
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
guest_ssh()  { ssh "${SSH_OPTS[@]}" "$SSH_TARGET" "$@"; }
guest_scp()  { scp "${SCP_OPTS[@]}" "$@"; }

plan() {
  cat <<EOF
profile:    $PROFILE  ($VM_LABEL)
disk:       $VM_DISK
snapshot:   $VM_SNAPSHOT        (restored before and after)
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

# Invoked only through the EXIT/INT/TERM trap below.
# shellcheck disable=SC2317
cleanup() {
  [ "$CLEANED" -eq 1 ] && return
  CLEANED=1
  if [ "$KEEP_VM" -eq 1 ]; then
    say "--keep-vm: leaving the VM running and the snapshot unrestored"
    return
  fi
  if [ -n "$QEMU_PID" ] && kill -0 "$QEMU_PID" 2>/dev/null; then
    say "shutting the VM down"
    guest_ssh "shutdown /s /t 0" >/dev/null 2>&1 || true
    for _ in $(seq 1 30); do
      kill -0 "$QEMU_PID" 2>/dev/null || break
      sleep 1
    done
    if kill -0 "$QEMU_PID" 2>/dev/null; then
      say "VM did not shut down; terminating qemu"
      kill -TERM "$QEMU_PID" 2>/dev/null || true
      sleep 3
      kill -KILL "$QEMU_PID" 2>/dev/null || true
    fi
  fi
  # Always leave the disk as it was found, even after a failure -- otherwise
  # the next run inherits Office's crash state and measures something else.
  if [ -n "${VM_DISK:-}" ] && [ -f "$VM_DISK" ]; then
    qemu-img snapshot -a "$VM_SNAPSHOT" "$VM_DISK" 2>/dev/null \
      || say "WARNING: could not restore snapshot $VM_SNAPSHOT -- the disk is dirty"
  fi
}
trap cleanup EXIT INT TERM

if [ "$DRY_RUN" -eq 1 ]; then
  plan
  say "--dry-run: validated the profile and the .pptx; nothing was booted"
  CLEANED=1
  exit 0
fi

for tool in qemu-system-x86_64 qemu-img ssh scp; do
  command -v "$tool" >/dev/null 2>&1 || infra "$tool is not installed"
done
[ -f "$VM_DISK" ] || infra "VM disk not found: $VM_DISK (provision it first; see README.md)"
[ -r "$VM_SSH_KEY" ] || infra "cannot read SSH key: $VM_SSH_KEY"
qemu-img snapshot -l "$VM_DISK" 2>/dev/null | grep -qw "$VM_SNAPSHOT" \
  || infra "snapshot '$VM_SNAPSHOT' not found on $VM_DISK"

mkdir -p "$ARTIFACTS" || infra "cannot create $ARTIFACTS"
rm -rf "${ARTIFACTS:?}/guest"

plan | tee "$ARTIFACTS/plan.txt" >&2

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
[ -n "$QEMU_PID" ] || infra "qemu started but wrote no pidfile"

say "waiting up to ${BOOT_TIMEOUT}s for guest SSH and an interactive session"
deadline=$(( $(date +%s) + BOOT_TIMEOUT ))
until guest_ssh "echo ready" >/dev/null 2>&1; do
  [ "$(date +%s)" -lt "$deadline" ] || infra "guest SSH never came up within ${BOOT_TIMEOUT}s"
  kill -0 "$QEMU_PID" 2>/dev/null || infra "the VM exited while booting (see $ARTIFACTS/qemu.log)"
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
  [ -n "$SLIDES" ] && echo "POWERPOINT_ACCEPTANCE_SLIDES=$SLIDES"
} > "$runfile"
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
  if [ "$(date +%s)" -ge "$deadline" ]; then
    say "guest test exceeded ${RUN_TIMEOUT}s"
    guest_scp -r "$SSH_TARGET:$GUEST_WORKDIR/artifacts" "$ARTIFACTS/guest" >/dev/null 2>&1 || true
    infra "the guest test did not finish; partial diagnostics in $ARTIFACTS/guest"
  fi
  kill -0 "$QEMU_PID" 2>/dev/null || infra "the VM died mid-test (see $ARTIFACTS/qemu.log)"
  sleep 5
done

say "collecting diagnostics"
guest_scp -r "$SSH_TARGET:$GUEST_WORKDIR/artifacts" "$ARTIFACTS/guest" >/dev/null 2>&1 \
  || say "WARNING: could not copy the guest's artifacts back"

if [ "$guest_status" -eq 0 ]; then
  say "RESULT: PowerPoint ($VM_LABEL) opened the file and every assertion passed"
else
  say "RESULT: the PowerPoint test RAN and FAILED (guest exit $guest_status)"
  say "        read $ARTIFACTS/guest for the dialog text, screenshots and slide exports"
fi
exit "$guest_status"
