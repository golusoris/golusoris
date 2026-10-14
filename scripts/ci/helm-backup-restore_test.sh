#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

if [[ "$(uname -s)" != Linux ]]; then
	printf 'helm-backup-restore: skip: %s runners run no Linux Docker daemon\n' "$(uname -s)"
	exit 0
fi
if ! command -v docker >/dev/null 2>&1; then
	printf 'helm-backup-restore: skip: docker is not installed on this runner\n'
	exit 0
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
readonly chart="$repo_root/deploy/helm"
readonly digest="sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

# shellcheck source=/dev/null
. "$repo_root/tools/tool-versions.env"
# shellcheck source=scripts/ci/lib/helm.sh
. "$repo_root/scripts/ci/lib/helm.sh"

helm_bin="$(resolve_pinned_helm "$HELM_VERSION")"
readonly helm_bin

work="$(mktemp -d)"
readonly work
net="golusoris-backup-restore-$$"
readonly net

cleanup() {
	local name
	for name in "$net-src" "$net-dst" "$net-s3"; do
		if docker container inspect "$name" >/dev/null 2>&1; then
			docker rm -f "$name" >/dev/null || printf 'helm-backup-restore: cleanup: cannot remove container %s\n' "$name" >&2
		fi
	done
	if docker network inspect "$net" >/dev/null 2>&1; then
		docker network rm "$net" >/dev/null || printf 'helm-backup-restore: cleanup: cannot remove network %s\n' "$net" >&2
	fi
	rm -rf -- "$work"
}
trap cleanup EXIT

common_args=(
	template golusoris "$chart"
	--set-string "image.digest=$digest"
)
backup_args=(
	"${common_args[@]}"
	--set backup.enabled=true
	--set-string backup.image.repository=registry.example.test/platform/postgres-backup
	--set-string "backup.image.digest=$digest"
	--set-string backup.s3.bucket=golusoris-backups
)

"$helm_bin" "${backup_args[@]}" \
	--show-only templates/backup-cronjob.yaml \
	--set-string backup.dbName=appdb \
	--set-string backup.s3.endpoint=http://s3:7070 > "$work/cronjob.yaml"

mkdir -p "$work/empty"
timeout 300 docker build -t golusoris-backup-restore:ci -f "$repo_root/scripts/ci/backup-restore.Dockerfile" "$work/empty" >/dev/null

cmd="$(timeout 300 docker run --rm -v "$work:$work:ro" golusoris-backup-restore:ci yq -o=json -I=0 'select(.kind == "CronJob") | .spec.jobTemplate.spec.template.spec.containers[0].command' "$work/cronjob.yaml")"
if [[ "$cmd" != '["/bin/sh","-c"]' ]]; then
	printf 'helm-backup-restore: container command is not ["/bin/sh","-c"]: %s\n' "$cmd" >&2
	exit 1
fi

timeout 300 docker run --rm -v "$work:$work:ro" golusoris-backup-restore:ci yq 'select(.kind == "CronJob") | .spec.jobTemplate.spec.template.spec.containers[0].args[0]' "$work/cronjob.yaml" > "$work/backup.sh"
if [[ ! -s "$work/backup.sh" ]]; then
	printf 'helm-backup-restore: backup script is empty\n' >&2
	exit 1
fi

actual_env="$(timeout 300 docker run --rm -v "$work:$work:ro" golusoris-backup-restore:ci yq 'select(.kind == "CronJob") | .spec.jobTemplate.spec.template.spec.containers[0].env[].name' "$work/cronjob.yaml" | sort)"
expected_env=$'AWS_ACCESS_KEY_ID\nAWS_DEFAULT_REGION\nAWS_SECRET_ACCESS_KEY\nBUCKET\nBUCKET_PREFIX\nDATABASE_URL\nDB_NAME\nRETENTION_DAYS\nS3_ENDPOINT'
if [[ "$actual_env" != "$expected_env" ]]; then
	printf 'helm-backup-restore: env drift guard failed: container env vars drift\nexpected:\n%s\nactual:\n%s\n' "$expected_env" "$actual_env" >&2
	exit 1
fi

bucket=""
bucket_prefix=""
db_name=""
s3_endpoint=""
retention_days=""
aws_default_region=""

while IFS='=' read -r name val; do
	case "$name" in
		BUCKET) bucket="$val" ;;
		BUCKET_PREFIX) bucket_prefix="$val" ;;
		DB_NAME) db_name="$val" ;;
		S3_ENDPOINT) s3_endpoint="$val" ;;
		RETENTION_DAYS) retention_days="$val" ;;
		AWS_DEFAULT_REGION) aws_default_region="$val" ;;
	esac
done < <(timeout 300 docker run --rm -v "$work:$work:ro" golusoris-backup-restore:ci yq 'select(.kind == "CronJob") | .spec.jobTemplate.spec.template.spec.containers[0].env[] | select(.value != null) | [.name, .value] | join("=")' "$work/cronjob.yaml")

if [[ -z "$bucket" || -z "$bucket_prefix" || -z "$db_name" || -z "$s3_endpoint" || -z "$retention_days" || -z "$aws_default_region" ]]; then
	printf 'helm-backup-restore: failed to extract required env values from rendered chart\n' >&2
	exit 1
fi

versity_image="$(sed -n 's/^[[:space:]]*VersityGW[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' "$repo_root/internal/testimages/images.go")"
if [[ -z "$versity_image" ]]; then
	printf 'helm-backup-restore: VersityGW image constant not found\n' >&2
	exit 1
fi

docker network create "$net" >/dev/null

timeout 300 docker run -d --name "$net-src" --network "$net" --network-alias src -e POSTGRES_PASSWORD=pw -e POSTGRES_DB=appdb golusoris-backup-restore:ci >/dev/null
timeout 300 docker run -d --name "$net-dst" --network "$net" --network-alias dst -e POSTGRES_PASSWORD=pw -e POSTGRES_DB=appdb golusoris-backup-restore:ci >/dev/null

aws_access_key_id="golusoris-test"
aws_secret_access_key="golusoris-test-secret"
timeout 300 docker run -d --name "$net-s3" --network "$net" --network-alias s3 \
	-e ROOT_ACCESS_KEY="$aws_access_key_id" \
	-e ROOT_SECRET_KEY="$aws_secret_access_key" \
	"$versity_image" \
	posix /tmp/vgw >/dev/null

for host in src dst; do
	ready=0
	for _ in $(seq 1 60); do
		if timeout 300 docker run --rm --network "$net" golusoris-backup-restore:ci pg_isready -h "$host" -U postgres >/dev/null 2>&1; then
			ready=1
			break
		fi
		sleep 1
	done
	if [[ "$ready" -ne 1 ]]; then
		printf 'helm-backup-restore: %s did not become ready within 60s\n' "$host" >&2
		exit 1
	fi
done

ready=0
for _ in $(seq 1 60); do
	if timeout 300 docker run --rm --network "$net" \
		-e AWS_ACCESS_KEY_ID="$aws_access_key_id" \
		-e AWS_SECRET_ACCESS_KEY="$aws_secret_access_key" \
		-e AWS_DEFAULT_REGION="$aws_default_region" \
		golusoris-backup-restore:ci \
		aws s3 ls --endpoint-url http://s3:7070 >/dev/null 2>&1; then
		ready=1
		break
	fi
	sleep 1
done
if [[ "$ready" -ne 1 ]]; then
	printf 'helm-backup-restore: s3 did not become ready within 60s\n' >&2
	exit 1
fi

cat <<'EOF' > "$work/seed.sql"
CREATE TABLE items (
    id bigint PRIMARY KEY,
    name text NOT NULL,
    note text
);

INSERT INTO items (id, name, note)
SELECT
    i,
    CASE
        WHEN i % 10 = 0 THEN 'item''s name with "quote", \backslash, ' || E'\nnewline and non-ASCII: \u00e9\u00e0\u00fc\u2713'
        ELSE 'item-' || i
    END,
    CASE
        WHEN i % 5 = 0 THEN NULL
        ELSE 'note-' || i
    END
FROM generate_series(1, 1000) AS i;

CREATE SEQUENCE test_seq;
SELECT setval('test_seq', 1234);

CREATE VIEW items_view AS SELECT id, name FROM items WHERE id <= 500;
EOF

timeout 300 docker run --rm -i --network "$net" \
	-e PGPASSWORD=pw \
	golusoris-backup-restore:ci \
	psql -v ON_ERROR_STOP=1 -h src -U postgres -d appdb < "$work/seed.sql" >/dev/null

timeout 300 docker run --rm --network "$net" \
	-e AWS_ACCESS_KEY_ID="$aws_access_key_id" \
	-e AWS_SECRET_ACCESS_KEY="$aws_secret_access_key" \
	-e AWS_DEFAULT_REGION="$aws_default_region" \
	golusoris-backup-restore:ci \
	aws s3 mb "s3://${bucket}" --endpoint-url "${s3_endpoint}" >/dev/null

# Same user, read-only root, capabilities and env as the CronJob pod.
backup_status=0
timeout 300 docker run --rm \
	--network "$net" \
	--read-only \
	--tmpfs /tmp:rw,mode=1777 \
	-u 65534:65534 \
	--cap-drop ALL \
	--security-opt no-new-privileges \
	-e AWS_ACCESS_KEY_ID="$aws_access_key_id" \
	-e AWS_DEFAULT_REGION="$aws_default_region" \
	-e AWS_SECRET_ACCESS_KEY="$aws_secret_access_key" \
	-e BUCKET="$bucket" \
	-e BUCKET_PREFIX="$bucket_prefix" \
	-e DATABASE_URL="postgres://postgres:pw@src:5432/appdb?sslmode=disable" \
	-e DB_NAME="$db_name" \
	-e RETENTION_DAYS="$retention_days" \
	-e S3_ENDPOINT="$s3_endpoint" \
	golusoris-backup-restore:ci \
	/bin/sh -c "$(cat "$work/backup.sh")" >"$work/backup.log" 2>&1 || backup_status=$?
if [[ "$backup_status" -ne 0 ]]; then
	printf 'helm-backup-restore: backup script exited %s:\n' "$backup_status" >&2
	tail -n 20 "$work/backup.log" >&2
	exit 1
fi

listing=""
if ! listing="$(timeout 300 docker run --rm --network "$net" \
	-e AWS_ACCESS_KEY_ID="$aws_access_key_id" \
	-e AWS_SECRET_ACCESS_KEY="$aws_secret_access_key" \
	-e AWS_DEFAULT_REGION="$aws_default_region" \
	golusoris-backup-restore:ci \
	aws s3 ls "s3://${bucket}/${bucket_prefix}/" --endpoint-url "${s3_endpoint}" 2>/dev/null)"; then
	listing=""
fi

keys="$(printf '%s\n' "$listing" | awk 'NF>=4 {print $4}' | sed '/^$/d')"
count="$(printf '%s\n' "$keys" | sed '/^$/d' | wc -l)"

if [[ "$count" -eq 0 ]]; then
	printf 'helm-backup-restore: no object under prefix s3://%s/%s/\n' "$bucket" "$bucket_prefix" >&2
	exit 1
elif [[ "$count" -ne 1 ]]; then
	printf 'helm-backup-restore: expected exactly one object under s3://%s/%s/, found %d: %s\n' "$bucket" "$bucket_prefix" "$count" "$listing" >&2
	exit 1
fi

key="$(printf '%s\n' "$keys" | head -n 1)"
pattern="^appdb-[0-9]{8}T[0-9]{6}Z\\.sql\\.gz$"
if [[ ! "$key" =~ $pattern ]]; then
	printf 'helm-backup-restore: object key %s does not match expected pattern %s\n' "$key" "$pattern" >&2
	exit 1
fi

timeout 300 docker run --rm --network "$net" \
	-e AWS_ACCESS_KEY_ID="$aws_access_key_id" \
	-e AWS_SECRET_ACCESS_KEY="$aws_secret_access_key" \
	-e AWS_DEFAULT_REGION="$aws_default_region" \
	golusoris-backup-restore:ci \
	aws s3 cp "s3://${bucket}/${bucket_prefix}/${key}" - --endpoint-url "${s3_endpoint}" > "$work/$key"

gzip -t "$work/$key"

gunzip -c "$work/$key" | timeout 300 docker run --rm -i --network "$net" \
	-e PGPASSWORD=pw \
	golusoris-backup-restore:ci \
	psql -v ON_ERROR_STOP=1 -h dst -U postgres -d appdb >/dev/null

query="SELECT count(*), coalesce(md5(string_agg(id||':'||name||':'||coalesce(note,''), ',' order by id)), ''), (SELECT last_value FROM test_seq), (SELECT count(*) FROM items_view) FROM items;"
src_stats="$(timeout 300 docker run --rm --network "$net" golusoris-backup-restore:ci psql -t -A -F'|' -v ON_ERROR_STOP=1 "postgres://postgres:pw@src:5432/appdb?sslmode=disable" -c "$query")"
dst_stats="$(timeout 300 docker run --rm --network "$net" golusoris-backup-restore:ci psql -t -A -F'|' -v ON_ERROR_STOP=1 "postgres://postgres:pw@dst:5432/appdb?sslmode=disable" -c "$query")"

IFS='|' read -r src_count src_hash src_seq src_view <<< "$src_stats"
IFS='|' read -r dst_count dst_hash dst_seq dst_view <<< "$dst_stats"

if [[ "$src_count" != "$dst_count" ]]; then
	printf 'helm-backup-restore: restored database differs: row count (src=%s dst=%s)\n' "$src_count" "$dst_count" >&2
	exit 1
fi
if [[ "$src_hash" != "$dst_hash" ]]; then
	printf 'helm-backup-restore: restored database differs: table checksum (src=%s dst=%s)\n' "$src_hash" "$dst_hash" >&2
	exit 1
fi
if [[ "$src_seq" != "$dst_seq" ]]; then
	printf 'helm-backup-restore: restored database differs: sequence last_value (src=%s dst=%s)\n' "$src_seq" "$dst_seq" >&2
	exit 1
fi
if [[ "$src_view" != "$dst_view" ]]; then
	printf 'helm-backup-restore: restored database differs: view row count (src=%s dst=%s)\n' "$src_view" "$dst_view" >&2
	exit 1
fi

printf 'helm-backup-restore: ok: %s rows restored from s3://%s/%s/%s\n' "$dst_count" "$bucket" "$bucket_prefix" "$key"
