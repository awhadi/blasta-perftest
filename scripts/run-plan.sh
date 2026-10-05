#!/usr/bin/env bash
# Run a template's enterprise test plan (jobs ent-01 ... ent-10) in order.
#
#   scripts/run-plan.sh wordpress --url https://staging.example.com --set post=hello-world
#   scripts/run-plan.sh redis --set host=redis.staging --steps 01,02,03
#   scripts/run-plan.sh keycloak --url https://kc.staging --set realm=app --time-scale 0.05   # dry run
#
# Options (everything else is passed to `blasta preset new`, e.g. --url, --set k=v):
#   --steps 01,02,03   run only these steps (default: all ten)
#   --extras           also run the ent-x-* scenario jobs
#   --time-scale F     shorten every run (0.05 = 20x shorter) to check the wiring first
#   --stop-on-fail     stop at the first missed gate (the smoke test always stops the plan)
#   --out DIR          where job files and results go (default: a temp directory)
#
# Exit status: 0 all gates met, 2 at least one gate missed, 1 could not run.
set -uo pipefail

BLASTA="${BLASTA:-blasta}"
steps="" extras=0 scale=1 stop=0 out="" pass=()
preset="${1:-}"; [[ -z "$preset" || "$preset" == -* ]] && { sed -n '2,16p' "$0"; exit 1; }
shift
while [[ $# -gt 0 ]]; do
  case "$1" in
    --steps) steps="$2"; shift 2 ;;
    --extras) extras=1; shift ;;
    --time-scale) scale="$2"; shift 2 ;;
    --stop-on-fail) stop=1; shift ;;
    --out) out="$2"; shift 2 ;;
    *) pass+=("$1"); shift ;;
  esac
done
[[ -z "$out" ]] && out="$(mktemp -d)"
mkdir -p "$out"

"$BLASTA" preset new "$preset" --out "$out" ${pass[@]+"${pass[@]}"} >/dev/null || { echo "could not render preset '$preset'" >&2; exit 1; }

files=()
for f in "$out/$preset"-ent-[0-9][0-9]-*.json; do
  [[ -e "$f" ]] || { echo "preset '$preset' has no enterprise plan" >&2; exit 1; }
  n="${f##*/$preset-ent-}"; n="${n%%-*}"
  [[ -n "$steps" && ",$steps," != *",$n,"* ]] && continue
  files+=("$f")
done
if [[ $extras -eq 1 ]]; then
  for f in "$out/$preset"-ent-x-*.json; do [[ -e "$f" ]] && files+=("$f"); done
fi

printf '\nEnterprise plan: %s  (%d steps, results in %s)\n\n' "$preset" "${#files[@]}" "$out"
failed=0 summary=()
for f in "${files[@]}"; do
  id="${f##*/$preset-}"; id="${id%.json}"
  printf '>> %s\n' "$id"
  "$BLASTA" run --time-scale "$scale" --json "$f" > "$out/$id.result.json" 2> "$out/$id.stderr"
  code=$?
  tail -n 2 "$out/$id.stderr" | sed 's/^/   /'
  case $code in
    0) summary+=("PASS  $id") ;;
    2) summary+=("FAIL  $id   ($(grep -h 'FAIL:' "$out/$id.stderr" | sed 's/^FAIL: thresholds not met: //'))"); failed=1
       [[ "$id" == ent-01-* ]] && { echo "smoke test failed: not applying real load." >&2; break; }
       [[ $stop -eq 1 ]] && break ;;
    *) summary+=("ERROR $id   ($(tail -n 1 "$out/$id.stderr"))"); failed=1; break ;;
  esac
done

printf '\n==== summary ====\n'
printf '%s\n' "${summary[@]}"
printf '\nreports: %s/*.result.json\n' "$out"
[[ $failed -eq 0 ]] && exit 0 || exit 2
