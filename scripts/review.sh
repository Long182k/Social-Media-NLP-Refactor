#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd -P)"

AGY_BIN="${AGY_BIN:-agy}"
WORKDIR="$(dirname -- "$REPO_ROOT")"
MODEL="gemini-4-argon-high"
REVIEW_SETTINGS='{"useG1Credits":false,"allowNonWorkspaceAccess":false,"toolPermission":"request-review","permissions":{"deny":["write_file(*)","command(*)","mcp(*)","read_url(*)","execute_url(*)"]}}'

OUT_FILE=""
PROMPT_TEXT=""
PROMPT_FILE=""
REQUIRE_APPROVED=0

usage() {
  cat <<'EOF'
Usage: review.sh --out <review.md> (--prompt <text> | --prompt-file <path>) [--workdir <dir>] [--require-approved]

Run a read-only Gemini 4 Argon High review through Antigravity with AI Credits off.

Options:
  -o, --out <path>          Required. Output .md file inside a dedicated review directory
                            (repo reviews/ or ~/.gemini/antigravity-cli/{brain,scratch}/).
  -p, --prompt <text>       Inline review prompt text (mutually exclusive with --prompt-file).
  -f, --prompt-file <path>  Path to file containing the review prompt (use '-' for stdin).
  -C, --workdir <dir>       Working directory for Antigravity (default: parent of the repo).
      --require-approved    Exit non-zero (3) unless verdict header is APPROVED / APPROVED FOR APPLY.
  -h, --help                Show this help message and exit.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    -o|--out)
      [[ $# -ge 2 ]] || { echo "Error: $1 requires a file path." >&2; exit 2; }
      OUT_FILE="$2"
      shift 2
      ;;
    -p|--prompt)
      [[ $# -ge 2 ]] || { echo "Error: $1 requires prompt text." >&2; exit 2; }
      PROMPT_TEXT="$2"
      shift 2
      ;;
    -f|--prompt-file)
      [[ $# -ge 2 ]] || { echo "Error: $1 requires a prompt file path." >&2; exit 2; }
      PROMPT_FILE="$2"
      shift 2
      ;;
    -C|--workdir)
      [[ $# -ge 2 ]] || { echo "Error: $1 requires a directory path." >&2; exit 2; }
      WORKDIR="$2"
      shift 2
      ;;
    --require-approved)
      REQUIRE_APPROVED=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Error: unknown option '$1'." >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ -z "$OUT_FILE" ]]; then
  echo "Error: --out <review.md> is required." >&2
  usage >&2
  exit 2
fi

if [[ -n "$PROMPT_TEXT" && -n "$PROMPT_FILE" ]]; then
  echo "Error: --prompt and --prompt-file are mutually exclusive." >&2
  exit 2
fi

if [[ -z "$PROMPT_TEXT" && -z "$PROMPT_FILE" ]]; then
  echo "Error: either --prompt or --prompt-file is required." >&2
  usage >&2
  exit 2
fi

if ! AGY_PATH="$(command -v -- "$AGY_BIN")"; then
  echo "Error: Antigravity CLI '$AGY_BIN' is unavailable." >&2
  exit 127
fi
AGY_BIN="$(realpath -- "$AGY_PATH")"

if ! command -v jq >/dev/null; then
  echo "Error: jq is required to read the Antigravity verdict." >&2
  exit 127
fi

if [[ ! -d "$WORKDIR" ]]; then
  echo "Error: workdir '$WORKDIR' does not exist." >&2
  exit 2
fi

if [[ -L "$OUT_FILE" ]]; then
  echo "Error: --out '$OUT_FILE' must not be a symlink." >&2
  exit 2
fi

OUT_REAL="$(realpath -m -- "$OUT_FILE")"
if [[ "$OUT_REAL" != *.md ]]; then
  echo "Error: --out '$OUT_REAL' must end with .md." >&2
  exit 2
fi

REPO_REVIEW_ROOT="$REPO_ROOT/reviews/"
AGY_BRAIN_ROOT="$HOME/.gemini/antigravity-cli/brain/"
AGY_SCRATCH_ROOT="$HOME/.gemini/antigravity-cli/scratch/"

if [[ "$OUT_REAL" == "$REPO_REVIEW_ROOT"*.md ]]; then
  :
elif [[ "$OUT_REAL" == "$AGY_BRAIN_ROOT"* || "$OUT_REAL" == "$AGY_SCRATCH_ROOT"* ]]; then
  OUT_PARENT_DIR="$(basename -- "$(dirname -- "$OUT_REAL")")"
  if [[ "$OUT_PARENT_DIR" != "reviews" ]]; then
    echo "Error: inside Antigravity state dirs, --out must be in a 'reviews/' subdir (got '$OUT_REAL')." >&2
    exit 2
  fi
else
  echo "Error: --out '$OUT_REAL' must reside inside '$REPO_REVIEW_ROOT' or an Antigravity review path ($AGY_BRAIN_ROOT / $AGY_SCRATCH_ROOT)." >&2
  exit 2
fi

if [[ -n "$PROMPT_FILE" && "$PROMPT_FILE" != "-" ]]; then
  [[ -r "$PROMPT_FILE" ]] || { echo "Error: prompt file '$PROMPT_FILE' is not readable." >&2; exit 2; }
  [[ -s "$PROMPT_FILE" ]] || { echo "Error: prompt file '$PROMPT_FILE' is empty." >&2; exit 2; }
  PROMPT_REAL="$(realpath -m -- "$PROMPT_FILE")"
  if [[ "$OUT_REAL" == "$PROMPT_REAL" ]]; then
    echo "Error: --out and --prompt-file must not refer to the same file ('$OUT_REAL')." >&2
    exit 2
  fi
fi

if [[ -n "$PROMPT_FILE" ]]; then
  if [[ "$PROMPT_FILE" == "-" ]]; then
    PROMPT_TEXT="$(cat)"
  else
    PROMPT_TEXT="$(cat -- "$PROMPT_FILE")"
  fi
fi
if [[ "$PROMPT_TEXT" != *[![:space:]]* ]]; then
  echo "Error: review prompt is empty." >&2
  exit 2
fi

OUT_DIR="$(dirname -- "$OUT_REAL")"
mkdir -p -- "$OUT_DIR"

TMP_OUT="$(mktemp -- "$OUT_DIR/.argon-review.XXXXXX.md")"
cleanup() {
  rm -f -- "$TMP_OUT"
}
trap cleanup EXIT

REVIEW_INSTRUCTIONS='You are the independent read-only reviewer. Use file-reading and search tools to inspect the requested files. Keep the review in your final reply and do not start other agents or change files. Start your final reply with exactly "# APPROVED" or "# CHANGES REQUESTED". Explain findings with file and line references after the verdict.'
if ! (
  cd -- "$WORKDIR"
  "$AGY_BIN" --model "$MODEL" --sandbox --settings "$REVIEW_SETTINGS" \
    --disable-slash-commands --output-format json --print-timeout 10m \
    --print "$REVIEW_INSTRUCTIONS"$'\n\n'"$PROMPT_TEXT"
) | jq -er 'select(.status == "SUCCESS") | .response | select(type == "string" and length > 0)' > "$TMP_OUT"; then
  echo "Error: Gemini Argon did not return a successful review. Existing report was left untouched." >&2
  exit 1
fi

FIRST_NONBLANK="$(awk '{ sub(/\r$/, ""); if (NF) { gsub(/^[[:space:]]+|[[:space:]]+$/, ""); print; exit } }' "$TMP_OUT")"
VERDICT_KIND=""
if [[ "$FIRST_NONBLANK" =~ ^(#+[[:space:]]+)?(APPROVED\ FOR\ APPLY|APPROVED)$ ]] || [[ "$FIRST_NONBLANK" =~ ^(#+[[:space:]]+)?\*\*(APPROVED\ FOR\ APPLY|APPROVED)\*\*$ ]]; then
  VERDICT_KIND="APPROVED"
elif [[ "$FIRST_NONBLANK" =~ ^(#+[[:space:]]+)?(CHANGES\ REQUESTED)$ ]] || [[ "$FIRST_NONBLANK" =~ ^(#+[[:space:]]+)?\*\*(CHANGES\ REQUESTED)\*\*$ ]]; then
  VERDICT_KIND="CHANGES_REQUESTED"
else
  echo "Error: First nonblank line ('$FIRST_NONBLANK') is not an exact recognized verdict header (APPROVED, APPROVED FOR APPLY, or CHANGES REQUESTED). Existing '$OUT_REAL' was left untouched." >&2
  exit 1
fi

mv -f -- "$TMP_OUT" "$OUT_REAL"
echo "Gemini Argon review saved to: $OUT_REAL"

if [[ "$REQUIRE_APPROVED" -eq 1 && "$VERDICT_KIND" != "APPROVED" ]]; then
  echo "Error: Gemini Argon verdict was '$FIRST_NONBLANK' (gate requires approval)." >&2
  exit 3
fi
