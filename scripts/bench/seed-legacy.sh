#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

if [[ -z "${LEGACY_DATABASE_URL:-}" ]]; then
  echo "ERROR: LEGACY_DATABASE_URL must be set (pointing to an isolated benchmark Postgres DB)." >&2
  echo "Example: LEGACY_DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5432/social_media_legacy?sslmode=disable' $0" >&2
  exit 1
fi

# Note: To push the legacy Prisma schema into LEGACY_DATABASE_URL without modifying
# /home/drakez/Projects/Social-Media-NLP, run from a separate directory:
#   DATABASE_URL="$LEGACY_DATABASE_URL" npx prisma db push --schema=/home/drakez/Projects/Social-Media-NLP/BE-Social-Media/prisma/schema.prisma

cd "${REPO_ROOT}/backend"
exec go run ./cmd/seed-legacy
