#!/usr/bin/env bash

set -Eeuo pipefail
IFS=$'\n\t'

REPOSITORY_ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
UPDATER="$REPOSITORY_ROOT/deploy/update.sh"

[[ $EUID -eq 0 ]] || {
  printf 'Ejecuta esta prueba con sudo.\n' >&2
  exit 1
}

TEST_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/jona-update-test.XXXXXXXX")

cleanup() {
  [[ "$TEST_ROOT" == "${TMPDIR:-/tmp}"/jona-update-test.* && -d "$TEST_ROOT" ]] \
    && rm -rf --one-file-system -- "$TEST_ROOT"
}
trap cleanup EXIT

fail_test() {
  printf 'Fallo: %s\n' "$*" >&2
  exit 1
}

assert_file_value() {
  local file=$1
  local expected=$2
  [[ -f "$file" ]] || fail_test "No existe $file"
  [[ "$(cat "$file")" == "$expected" ]] || fail_test "Contenido inesperado en $file"
}

FAKE_BIN="$TEST_ROOT/bin"
mkdir -m 0755 "$FAKE_BIN"

cat > "$FAKE_BIN/systemctl" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
printf '%s\n' "$*" >> "$JONA_TEST_LOG"
case "${1-}" in
  is-active)
    [[ "$(cat "$JONA_TEST_SERVICE_STATE")" == 'active' ]]
    ;;
  stop)
    printf 'stopped\n' > "$JONA_TEST_SERVICE_STATE"
    ;;
  start)
    printf 'active\n' > "$JONA_TEST_SERVICE_STATE"
    if [[ -f "$JONA_TEST_CURRENT/RELEASE_VERSION" \
      && "$(cat "$JONA_TEST_CURRENT/RELEASE_VERSION")" == "$JONA_TEST_LATEST_VERSION" ]]; then
      printf 'migrated\n' >> "$JONA_TEST_DATA/database.sqlite"
    fi
    ;;
  *) exit 2 ;;
esac
EOF

cat > "$FAKE_BIN/curl" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
[[ "$(cat "$JONA_TEST_SERVICE_STATE")" == 'active' ]] || exit 22
if [[ "$JONA_TEST_HEALTH_MODE" == 'rollback' \
  && -f "$JONA_TEST_CURRENT/RELEASE_VERSION" \
  && "$(cat "$JONA_TEST_CURRENT/RELEASE_VERSION")" == "$JONA_TEST_LATEST_VERSION" ]]; then
  exit 22
fi
printf '{"status":"ok"}\n'
EOF

cat > "$FAKE_BIN/sleep" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF

chmod 0755 "$FAKE_BIN/systemctl" "$FAKE_BIN/curl" "$FAKE_BIN/sleep"

create_fixture() {
  local name=$1
  local old_version=$2
  local new_version=$3
  local fixture="$TEST_ROOT/$name"
  local install_root="$fixture/install"
  local data_root="$fixture/data"
  local backup_root="$fixture/backups"
  local package_root="$fixture/package"

  mkdir -p "$install_root/releases/$old_version/server" "$data_root" "$backup_root" \
    "$package_root/server" "$package_root/deploy"
  printf '%s\n' "$old_version" > "$install_root/releases/$old_version/RELEASE_VERSION"
  printf 'old server\n' > "$install_root/releases/$old_version/server/index.mjs"
  ln -s "$install_root/releases/$old_version" "$install_root/current"
  printf 'original\n' > "$data_root/database.sqlite"

  printf '%s\n' "$new_version" > "$package_root/RELEASE_VERSION"
  printf 'new server\n' > "$package_root/server/index.mjs"
  install -m 0755 "$UPDATER" "$package_root/update.sh"
  install -m 0644 "$REPOSITORY_ROOT/README.md" "$package_root/README.md"
  install -m 0644 "$REPOSITORY_ROOT/deploy/homelab.env.example" "$package_root/deploy/homelab.env.example"
  install -m 0644 "$REPOSITORY_ROOT/deploy/jona-homelab.service" "$package_root/deploy/jona-homelab.service"

  tar -czf "$fixture/jona-homelab.tar.gz" --directory "$package_root" .
  (cd "$fixture" && sha256sum jona-homelab.tar.gz > jona-homelab.tar.gz.sha256)
  cat > "$fixture/release.json" <<EOF
{"tag_name":"$new_version","assets":[
  {"name":"jona-homelab.tar.gz","browser_download_url":"$fixture/jona-homelab.tar.gz"},
  {"name":"jona-homelab.tar.gz.sha256","browser_download_url":"$fixture/jona-homelab.tar.gz.sha256"},
  {"name":"update.sh","browser_download_url":"$UPDATER"}
]}
EOF
  printf 'active\n' > "$fixture/service-state"
  : > "$fixture/systemctl.log"
  printf '%s\n' "$fixture"
}

run_updater() {
  local fixture=$1
  local new_version=$2
  local health_mode=$3
  env \
    PATH="$FAKE_BIN:$PATH" \
    JONA_TEST_LOG="$fixture/systemctl.log" \
    JONA_TEST_SERVICE_STATE="$fixture/service-state" \
    JONA_TEST_HEALTH_MODE="$health_mode" \
    JONA_TEST_CURRENT="$fixture/install/current" \
    JONA_TEST_LATEST_VERSION="$new_version" \
    JONA_TEST_DATA="$fixture/data" \
    bash "$UPDATER" \
      --install-root "$fixture/install" \
      --data-root "$fixture/data" \
      --backup-root "$fixture/backups" \
      --service jona-homelab-test \
      --health-url http://127.0.0.1:3999/api/health \
      --release-api "$fixture/release.json"
}

old_version='main-000000000000'

same_fixture=$(create_fixture same-version "$old_version" "$old_version")
run_updater "$same_fixture" "$old_version" success >/dev/null
grep -Eq '^stop( |$)' "$same_fixture/systemctl.log" \
  && fail_test 'Misma versión detuvo servicio.'

bad_version='main-111111111111'
bad_fixture=$(create_fixture bad-checksum "$old_version" "$bad_version")
printf '%064d  jona-homelab.tar.gz\n' 0 > "$bad_fixture/jona-homelab.tar.gz.sha256"
if run_updater "$bad_fixture" "$bad_version" success >/dev/null 2>&1; then
  fail_test 'Checksum inválido fue aceptado.'
fi
grep -Eq '^stop( |$)' "$bad_fixture/systemctl.log" \
  && fail_test 'Checksum inválido detuvo servicio.'

malformed_version='main-444444444444'
malformed_fixture=$(create_fixture malformed "$old_version" "$malformed_version")
rm -f "$malformed_fixture/package/server/index.mjs"
tar -czf "$malformed_fixture/jona-homelab.tar.gz" --directory "$malformed_fixture/package" .
(cd "$malformed_fixture" && sha256sum jona-homelab.tar.gz > jona-homelab.tar.gz.sha256)
if run_updater "$malformed_fixture" "$malformed_version" success >/dev/null 2>&1; then
  fail_test 'Paquete incompleto fue aceptado.'
fi
grep -Eq '^stop( |$)' "$malformed_fixture/systemctl.log" \
  && fail_test 'Paquete incompleto detuvo servicio.'

success_version='main-222222222222'
success_fixture=$(create_fixture success "$old_version" "$success_version")
run_updater "$success_fixture" "$success_version" success >/dev/null
[[ "$(readlink -f "$success_fixture/install/current")" == "$success_fixture/install/releases/$success_version" ]] \
  || fail_test 'Actualización correcta no activó release nueva.'
assert_file_value "$success_fixture/data/database.sqlite" $'original\nmigrated'
[[ $(find "$success_fixture/backups" -maxdepth 1 -name '*.tar.gz' | wc -l) -eq 1 ]] \
  || fail_test 'Actualización correcta no conservó exactamente un backup.'

rollback_version='main-333333333333'
rollback_fixture=$(create_fixture rollback "$old_version" "$rollback_version")
if run_updater "$rollback_fixture" "$rollback_version" rollback >/dev/null 2>&1; then
  fail_test 'Health check fallido devolvió éxito.'
fi
[[ "$(readlink -f "$rollback_fixture/install/current")" == "$rollback_fixture/install/releases/$old_version" ]] \
  || fail_test 'Rollback no restauró release anterior.'
assert_file_value "$rollback_fixture/data/database.sqlite" original
failed_database=$(find "$rollback_fixture/backups" -path '*/failed-data-*/database.sqlite' -print -quit)
assert_file_value "$failed_database" $'original\nmigrated'

printf 'Updater: 5 escenarios correctos.\n'
