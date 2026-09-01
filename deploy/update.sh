#!/usr/bin/env bash

set -Eeuo pipefail
IFS=$'\n\t'

INSTALL_ROOT='/opt/jona-homelab'
DATA_ROOT='/var/lib/jona-homelab'
BACKUP_ROOT='/var/backups/jona-homelab'
SERVICE='jona-homelab'
HEALTH_URL='http://127.0.0.1:3000/api/health'
RELEASE_API='https://api.github.com/repos/jonatancheca/jona-homelab/releases/latest'

ARCHIVE_NAME='jona-homelab.tar.gz'
CHECKSUM_NAME='jona-homelab.tar.gz.sha256'
UPDATER_NAME='update.sh'

TEMP_DIRECTORY=''
OLD_RELEASE=''
NEW_RELEASE=''
BACKUP_FILE=''
FAILED_DATA_DIRECTORY=''
SERVICE_STOPPED=0
LINK_SWITCHED=0
NEW_SERVICE_ATTEMPTED=0
UPDATE_SUCCEEDED=0

usage() {
  cat <<'EOF'
Uso: update.sh [opciones]

Opciones:
  --install-root RUTA  Instalación (por defecto: /opt/jona-homelab)
  --data-root RUTA     Datos persistentes (por defecto: /var/lib/jona-homelab)
  --backup-root RUTA   Backups (por defecto: /var/backups/jona-homelab)
  --service NOMBRE     Unidad systemd (por defecto: jona-homelab)
  --health-url URL     Health check (por defecto: http://127.0.0.1:3000/api/health)
  --release-api URL    API de latest release o JSON local para pruebas
  --help               Mostrar ayuda
EOF
}

fail() {
  printf 'Error: %s\n' "$*" >&2
  exit 1
}

require_value() {
  local option=$1
  local value=${2-}
  [[ -n "$value" ]] || fail "$option requiere un valor."
}

parse_arguments() {
  while (($#)); do
    case "$1" in
      --install-root)
        require_value "$1" "${2-}"
        INSTALL_ROOT=$2
        shift 2
        ;;
      --data-root)
        require_value "$1" "${2-}"
        DATA_ROOT=$2
        shift 2
        ;;
      --backup-root)
        require_value "$1" "${2-}"
        BACKUP_ROOT=$2
        shift 2
        ;;
      --service)
        require_value "$1" "${2-}"
        SERVICE=$2
        shift 2
        ;;
      --health-url)
        require_value "$1" "${2-}"
        HEALTH_URL=$2
        shift 2
        ;;
      --release-api)
        require_value "$1" "${2-}"
        RELEASE_API=$2
        shift 2
        ;;
      --help|-h)
        usage
        exit 0
        ;;
      *) fail "Opción desconocida: $1" ;;
    esac
  done
}

require_commands() {
  local command
  for command in awk basename cat chmod curl date dirname find grep install ln mkdir mktemp mv node readlink rm sha256sum sleep systemctl tar; do
    command -v "$command" >/dev/null 2>&1 || fail "Falta comando requerido: $command"
  done
}

canonical_existing_directory() {
  local path=$1
  [[ "$path" == /* ]] || fail "Ruta debe ser absoluta: $path"
  [[ -d "$path" ]] || fail "No existe directorio: $path"
  [[ ! -L "$path" ]] || fail "No se admite enlace como directorio raíz: $path"
  readlink -f -- "$path"
}

assert_direct_child() {
  local child=$1
  local parent=$2
  [[ "$(dirname -- "$child")" == "$parent" ]] || fail "Ruta fuera de directorio permitido: $child"
}

receive_file() {
  local source=$1
  local destination=$2
  local timeout=$3
  if [[ -f "$source" ]]; then
    install -m 0600 -- "$source" "$destination"
    return
  fi
  curl --fail --location --silent --show-error --retry 3 \
    --connect-timeout 15 --max-time "$timeout" \
    -H 'Accept: application/vnd.github+json' \
    -H 'User-Agent: jona-homelab-updater' \
    --output "$destination" "$source"
}

parse_release() {
  local release_file=$1
  local metadata_file=$2
  node - "$release_file" "$ARCHIVE_NAME" "$CHECKSUM_NAME" "$UPDATER_NAME" > "$metadata_file" <<'NODE'
const fs = require('node:fs')

const [releasePath, archiveName, checksumName, updaterName] = process.argv.slice(2)
const release = JSON.parse(fs.readFileSync(releasePath, 'utf8'))
if (typeof release.tag_name !== 'string' || !/^main-[0-9a-f]{12}$/.test(release.tag_name)) {
  throw new Error('La latest release no tiene una versión main-<sha> válida.')
}
if (!Array.isArray(release.assets)) throw new Error('La latest release no contiene assets.')

function assetUrl(name) {
  const matches = release.assets.filter(asset => asset && asset.name === name)
  if (matches.length !== 1 || typeof matches[0].browser_download_url !== 'string' || !matches[0].browser_download_url) {
    throw new Error(`La latest release no contiene exactamente un asset ${name}.`)
  }
  return matches[0].browser_download_url
}

process.stdout.write([
  release.tag_name,
  assetUrl(archiveName),
  assetUrl(checksumName),
  assetUrl(updaterName),
].join('\n') + '\n')
NODE
}

validate_archive() {
  local archive=$1
  local checksum=$2
  local version=$3
  local entries_file=$4
  local normalized_file=$5
  local details_file=$6
  local expected_hash actual_hash entry clean type

  expected_hash=$(awk 'NR == 1 { print toupper($1) }' "$checksum")
  [[ "$expected_hash" =~ ^[A-F0-9]{64}$ ]] || fail 'Checksum publicado no válido.'
  actual_hash=$(sha256sum "$archive" | awk '{ print toupper($1) }')
  [[ "$actual_hash" == "$expected_hash" ]] || fail "Checksum de $ARCHIVE_NAME no coincide."

  tar -tzf "$archive" > "$entries_file"
  : > "$normalized_file"
  while IFS= read -r entry; do
    clean=${entry#./}
    [[ -z "$clean" ]] && continue
    [[ "$clean" != /* ]] || fail "Ruta absoluta dentro del artefacto: $entry"
    [[ "/$clean/" != *'/../'* ]] || fail "Ruta fuera del artefacto: $entry"
    printf '%s\n' "$clean" >> "$normalized_file"
  done < "$entries_file"

  LC_ALL=C tar -tzvf "$archive" > "$details_file"
  while IFS= read -r entry; do
    type=${entry:0:1}
    [[ "$type" == '-' || "$type" == 'd' ]] || fail 'Artefacto contiene enlaces o entradas especiales.'
  done < "$details_file"

  local required
  for required in \
    RELEASE_VERSION \
    server/index.mjs \
    update.sh \
    README.md \
    deploy/homelab.env.example \
    deploy/jona-homelab.service; do
    grep -Fxq "$required" "$normalized_file" || fail "Artefacto no contiene $required."
  done

  mkdir -m 0755 "$TEMP_DIRECTORY/extracted"
  tar -xzf "$archive" --directory "$TEMP_DIRECTORY/extracted" --no-same-owner --no-same-permissions
  [[ -z "$(find "$TEMP_DIRECTORY/extracted" -type l -print -quit)" ]] || fail 'Artefacto extraído contiene enlaces.'
  [[ "$(cat "$TEMP_DIRECTORY/extracted/RELEASE_VERSION")" == "$version" ]] || fail 'RELEASE_VERSION no coincide con latest release.'
  chmod -R a+rX "$TEMP_DIRECTORY/extracted"
  chmod 0755 "$TEMP_DIRECTORY/extracted/update.sh"
}

wait_for_health() {
  local response attempt
  for ((attempt = 1; attempt <= 120; attempt++)); do
    if response=$(curl --fail --silent --show-error --max-time 3 "$HEALTH_URL" 2>/dev/null) \
      && [[ "$response" =~ \"status\"[[:space:]]*:[[:space:]]*\"ok\" ]]; then
      return 0
    fi
    sleep 0.5
  done
  return 1
}

switch_current_link() {
  local target=$1
  local pending_link="$INSTALL_ROOT/.current-$OPERATION_ID"
  assert_direct_child "$pending_link" "$INSTALL_ROOT"
  [[ ! -e "$pending_link" && ! -L "$pending_link" ]] || fail "Enlace temporal ya existe: $pending_link"
  ln -s -- "$target" "$pending_link"
  if ! mv -Tf -- "$pending_link" "$INSTALL_ROOT/current"; then
    rm -f -- "$pending_link"
    return 1
  fi
}

restore_after_failure() {
  local restored=0
  set +e
  printf 'Actualización falló; iniciando rollback.\n' >&2

  if ((NEW_SERVICE_ATTEMPTED)); then
    if ! systemctl stop "$SERVICE" >/dev/null 2>&1 \
      || systemctl is-active --quiet "$SERVICE"; then
      printf 'Rollback: servicio nuevo no se pudo detener; datos no modificados.\n' >&2
      return 1
    fi
  fi

  if ((NEW_SERVICE_ATTEMPTED)) && [[ -n "$BACKUP_FILE" && -f "$BACKUP_FILE" ]]; then
    if [[ -d "$DATA_ROOT" ]]; then
      if ! mv -- "$DATA_ROOT" "$FAILED_DATA_DIRECTORY"; then
        printf 'Rollback: no se pudo conservar datos fallidos en %s.\n' "$FAILED_DATA_DIRECTORY" >&2
        return 1
      fi
    fi
    if ! tar -xzf "$BACKUP_FILE" --directory "$(dirname -- "$DATA_ROOT")" --same-owner; then
      printf 'Rollback: no se pudo restaurar backup %s.\n' "$BACKUP_FILE" >&2
      return 1
    fi
  fi

  if ((LINK_SWITCHED)); then
    if ! switch_current_link "$OLD_RELEASE"; then
      printf 'Rollback: no se pudo restaurar enlace current.\n' >&2
      return 1
    fi
  fi

  if ((SERVICE_STOPPED)); then
    if systemctl start "$SERVICE" && wait_for_health; then
      restored=1
    fi
  else
    restored=1
  fi

  if ((restored)); then
    printf 'Rollback completado. Backup: %s\n' "${BACKUP_FILE:-no creado}" >&2
    [[ -n "$FAILED_DATA_DIRECTORY" && -d "$FAILED_DATA_DIRECTORY" ]] \
      && printf 'Datos fallidos conservados: %s\n' "$FAILED_DATA_DIRECTORY" >&2
    return 0
  fi

  printf 'Rollback incompleto; servicio anterior no superó health check.\n' >&2
  return 1
}

cleanup() {
  local status=$?
  trap - EXIT INT TERM

  if ((status != 0 && UPDATE_SUCCEEDED == 0 && SERVICE_STOPPED == 1)); then
    restore_after_failure || true
  fi

  if [[ -n "$TEMP_DIRECTORY" && -d "$TEMP_DIRECTORY" ]]; then
    if [[ "$(dirname -- "$TEMP_DIRECTORY")" == "$INSTALL_ROOT" && "$(basename -- "$TEMP_DIRECTORY")" == .update-* ]]; then
      rm -rf --one-file-system -- "$TEMP_DIRECTORY"
    else
      printf 'No se limpia ruta temporal inesperada: %s\n' "$TEMP_DIRECTORY" >&2
    fi
  fi
  exit "$status"
}

main() {
  parse_arguments "$@"
  [[ $EUID -eq 0 ]] || fail 'Ejecuta actualizador como root.'
  require_commands

  [[ "$SERVICE" =~ ^[A-Za-z0-9_.@-]+$ ]] || fail 'Nombre de servicio no válido.'
  if [[ "$HEALTH_URL" =~ ^http://(127\.0\.0\.1|localhost):([0-9]{1,5})(/[^[:space:]]*)?$ ]]; then
    local health_port=${BASH_REMATCH[2]}
    ((health_port >= 1 && health_port <= 65535)) || fail 'Puerto de health-url no válido.'
  else
    fail 'health-url debe usar HTTP sobre loopback.'
  fi

  INSTALL_ROOT=$(canonical_existing_directory "$INSTALL_ROOT")
  DATA_ROOT=$(canonical_existing_directory "$DATA_ROOT")
  [[ "$INSTALL_ROOT" != '/' && "$DATA_ROOT" != '/' ]] || fail 'No se admite raíz del sistema.'

  [[ "$BACKUP_ROOT" == /* && "$BACKUP_ROOT" != '/' ]] || fail 'backup-root debe ser ruta absoluta concreta.'
  [[ ! -L "$BACKUP_ROOT" ]] || fail "No se admite enlace como backup-root: $BACKUP_ROOT"
  install -d -m 0700 -- "$BACKUP_ROOT"
  BACKUP_ROOT=$(canonical_existing_directory "$BACKUP_ROOT")
  [[ "$BACKUP_ROOT" != "$DATA_ROOT" && "$BACKUP_ROOT" != "$DATA_ROOT"/* ]] \
    || fail 'backup-root no puede estar dentro de data-root.'

  local releases_root="$INSTALL_ROOT/releases"
  releases_root=$(canonical_existing_directory "$releases_root")
  [[ -L "$INSTALL_ROOT/current" ]] || fail 'current debe ser un enlace simbólico.'
  OLD_RELEASE=$(readlink -f -- "$INSTALL_ROOT/current")
  [[ -d "$OLD_RELEASE" ]] || fail 'current no apunta a una release válida.'
  assert_direct_child "$OLD_RELEASE" "$releases_root"

  systemctl is-active --quiet "$SERVICE" || fail "Servicio no está activo: $SERVICE"

  trap cleanup EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM

  TEMP_DIRECTORY=$(mktemp -d "$INSTALL_ROOT/.update-XXXXXXXX")
  assert_direct_child "$TEMP_DIRECTORY" "$INSTALL_ROOT"
  local release_file="$TEMP_DIRECTORY/release.json"
  local metadata_file="$TEMP_DIRECTORY/release.metadata"
  receive_file "$RELEASE_API" "$release_file" 30
  parse_release "$release_file" "$metadata_file"

  local version archive_url checksum_url updater_url
  mapfile -t release_metadata < "$metadata_file"
  ((${#release_metadata[@]} == 4)) || fail 'Metadatos de release incompletos.'
  version=${release_metadata[0]}
  archive_url=${release_metadata[1]}
  checksum_url=${release_metadata[2]}
  updater_url=${release_metadata[3]}
  [[ -n "$updater_url" ]] || fail "Asset $UPDATER_NAME no válido."

  local current_version=''
  [[ -f "$OLD_RELEASE/RELEASE_VERSION" ]] && current_version=$(cat "$OLD_RELEASE/RELEASE_VERSION")
  if [[ "$current_version" == "$version" ]]; then
    printf 'Ya está instalada última versión: %s\n' "$version"
    UPDATE_SUCCEEDED=1
    return 0
  fi

  NEW_RELEASE="$releases_root/$version"
  assert_direct_child "$NEW_RELEASE" "$releases_root"
  [[ ! -e "$NEW_RELEASE" ]] || fail "Release ya existe y no está activa: $NEW_RELEASE"

  local archive="$TEMP_DIRECTORY/$ARCHIVE_NAME"
  local checksum="$TEMP_DIRECTORY/$CHECKSUM_NAME"
  printf 'Descargando %s...\n' "$version"
  receive_file "$archive_url" "$archive" 300
  receive_file "$checksum_url" "$checksum" 30
  validate_archive "$archive" "$checksum" "$version" \
    "$TEMP_DIRECTORY/archive.entries" "$TEMP_DIRECTORY/archive.normalized" "$TEMP_DIRECTORY/archive.details"

  printf 'Deteniendo %s y creando backup...\n' "$SERVICE"
  SERVICE_STOPPED=1
  systemctl stop "$SERVICE"
  systemctl is-active --quiet "$SERVICE" && fail "Servicio no se detuvo: $SERVICE"

  local timestamp data_parent data_name
  timestamp=$(date -u +%Y%m%dT%H%M%SZ)
  data_parent=$(dirname -- "$DATA_ROOT")
  data_name=$(basename -- "$DATA_ROOT")
  BACKUP_FILE="$BACKUP_ROOT/${version}-${timestamp}-${OPERATION_ID}.tar.gz"
  FAILED_DATA_DIRECTORY="$BACKUP_ROOT/failed-data-${version}-${timestamp}-${OPERATION_ID}"
  assert_direct_child "$BACKUP_FILE" "$BACKUP_ROOT"
  assert_direct_child "$FAILED_DATA_DIRECTORY" "$BACKUP_ROOT"
  tar -czf "$BACKUP_FILE" --directory "$data_parent" "$data_name"
  chmod 0600 "$BACKUP_FILE"

  mv -- "$TEMP_DIRECTORY/extracted" "$NEW_RELEASE"
  switch_current_link "$NEW_RELEASE"
  LINK_SWITCHED=1
  NEW_SERVICE_ATTEMPTED=1
  systemctl start "$SERVICE"
  wait_for_health || fail "Nueva versión no respondió correctamente en $HEALTH_URL"

  UPDATE_SUCCEEDED=1
  SERVICE_STOPPED=0
  printf 'Actualización completada: %s\n' "$version"
  printf 'Backup conservado: %s\n' "$BACKUP_FILE"
}

OPERATION_ID="$(date -u +%Y%m%d%H%M%S)-$$-$RANDOM"
main "$@"
