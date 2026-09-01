# Jona Homelab

Panel privado en español para registrar ordenadores y enviarles paquetes Wake-on-LAN desde un Ubuntu siempre encendido en su misma subred.

Nuxt 4 + SQLite integrado en Node. Un proceso para la aplicación y otro para `cloudflared`. Sin Docker, ORM, servidor de base de datos, Nginx, PM2 ni ejecutables de Wake-on-LAN. No instala nada en los equipos destino.

## Requisitos

- Ubuntu 26.04, Node **24 LTS ≥24.15** y acceso a Internet para Cloudflare.
- Node incluye `node:sqlite`; esa API sigue siendo *release candidate*. El proyecto fija la línea 24 y el lockfile; las actualizaciones deben pasar los tests.
- Equipos en la misma subred IPv4, conectados por Ethernet, con alimentación y Wake-on-LAN habilitado en BIOS/UEFI y en el sistema operativo. Algunos equipos no despiertan desde apagado completo; revisa las opciones de ahorro de energía e inicio rápido del fabricante.
- Un dominio gestionado por Cloudflare, una aplicación Access y un Tunnel. No hay acceso directo por IP desde la LAN: usa el dominio protegido también desde casa.

«Paquete enviado» significa que el sistema operativo aceptó el datagrama UDP. **No confirma entrega ni arranque.** No se incluyen ping, apagado, escaneo de red o programación.

## Desarrollo

Con Node 24 LTS y pnpm 11.2.0:

```sh
pnpm install --frozen-lockfile
cp .env.example .env
pnpm dev
```

Abre <http://127.0.0.1:3000>. En PowerShell, usa `Copy-Item .env.example .env` en lugar de `cp` si lo prefieres. No sobrescribas un `.env` existente sin revisarlo.

Durante el desarrollo, el servidor escucha en `127.0.0.1:3000` por defecto. No uses un servidor de desarrollo como origen de un Tunnel.

**El botón Encender en desarrollo envía paquetes reales según `.env`.** Para pruebas sin tocar tu LAN, configura `WOL_BROADCAST=127.0.0.1` y `WOL_SOURCE_IP=127.0.0.1`, o ejecuta los tests aislados.

## Validación

```sh
pnpm test
pnpm lint
pnpm typecheck
pnpm build
pnpm exec playwright install chromium
pnpm test:e2e
pnpm test:production
```

Los tests unitarios verifican MAC, CRUD, persistencia, paquetes, errores UDP y límites. Los E2E levantan una instancia aislada en loopback, con una SQLite temporal, y mandan UDP exclusivamente a loopback. El test de producción arranca el resultado compilado y comprueba el acceso local detrás del Tunnel y el rechazo del bypass. No hacen broadcasts hacia ordenadores reales. Playwright y las herramientas de desarrollo **no se despliegan** al Ubuntu de producción.

Detén `pnpm dev` antes de ejecutar los E2E: Nuxt no admite dos servidores de desarrollo simultáneos sobre este mismo proyecto. Los tests usan los puertos 3123 (E2E) y 3124 (producción), que deben estar libres.

## Instalación en Ubuntu

### 1. Preparar Node y el artefacto

Instala Node 24 LTS desde la [distribución oficial de Node](https://nodejs.org/en/download). Comprueba `node --version` y `command -v node`. La unidad incluida usa `/usr/bin/node`; si la ruta difiere, sustituye **solo `ExecStart`** por la ruta absoluta del binario de Node del sistema. Evita instalaciones bajo el directorio personal: el servicio no tiene acceso a `/home`.

En el equipo de compilación, instala con el lockfile y ejecuta las validaciones. Para una entrega Ubuntu reproducible, compila en Linux con la misma arquitectura que el destino (Ubuntu/WSL o CI Linux); no copies `node_modules` de Windows.

```sh
pnpm install --frozen-lockfile
pnpm build
mkdir -p artifacts
tar -czf artifacts/jona-homelab.tar.gz -C .output .
```

Cada commit que llega a `main` genera además una release oficial `main-<sha>` desde GitHub Actions. La release contiene:

- `jona-homelab.tar.gz`: runtime de `.output`, `RELEASE_VERSION`, actualizador, README y archivos de despliegue.
- `jona-homelab.tar.gz.sha256`: checksum obligatorio del paquete.
- `update.sh`: copia independiente para incorporar el actualizador a instalaciones antiguas.

La release se compila en Linux y no incluye Node. No hacen falta fuentes, pnpm ni herramientas de compilación en producción.

### 2. Usuario, archivos y configuración

Descarga y verifica la última release directamente en Ubuntu:

```sh
release_url=https://github.com/jonatancheca/jona-homelab/releases/latest/download
curl --fail --location --remote-name "$release_url/jona-homelab.tar.gz"
curl --fail --location --remote-name "$release_url/jona-homelab.tar.gz.sha256"
sha256sum --check jona-homelab.tar.gz.sha256
version="$(tar -xOf jona-homelab.tar.gz ./RELEASE_VERSION | tr -d '\r\n')"
printf '%s\n' "$version" | grep -Eq '^main-[0-9a-f]{12}$'
```

Después prepara instalación inicial. Si usuario o directorios ya existen, no los vuelvas a crear ni reemplaces configuración existente.

Si la aplicación ya está instalada, **no repitas esta instalación inicial ni borres lo existente**. Ve directamente a [Actualizaciones y rollback](#actualizaciones-y-rollback): el actualizador conserva base de datos, configuración, backups y releases anteriores.

```sh
id -u jona-homelab >/dev/null 2>&1 || sudo useradd --system --user-group --home-dir /var/lib/jona-homelab --no-create-home --shell /usr/sbin/nologin jona-homelab
sudo install -d -m 0755 /opt/jona-homelab/releases
sudo install -d -m 0755 "/opt/jona-homelab/releases/$version"
sudo tar -xzf jona-homelab.tar.gz -C "/opt/jona-homelab/releases/$version" --no-same-owner --no-same-permissions
sudo chmod -R a+rX "/opt/jona-homelab/releases/$version"
sudo chmod 0755 "/opt/jona-homelab/releases/$version/update.sh"
sudo ln -s "/opt/jona-homelab/releases/$version" /opt/jona-homelab/current
sudo install -m 0600 /opt/jona-homelab/current/deploy/homelab.env.example /etc/jona-homelab.env
sudo install -m 0644 /opt/jona-homelab/current/deploy/jona-homelab.service /etc/systemd/system/jona-homelab.service
sudoedit /etc/jona-homelab.env
```

Si el usuario ya existe, no vuelvas a crearlo. Los comandos de instalación de configuración son **solo para la primera instalación**: nunca sobreescribas el archivo existente durante una actualización.

`DB_PATH` apunta al archivo persistente; systemd crea `/var/lib/jona-homelab` con permisos privados. No guardes secretos ni la SQLite dentro del repositorio o del directorio de versiones.

`WOL_BROADCAST` vale `255.255.255.255` por defecto. Si el servidor tiene varias interfaces, establece el broadcast correcto de la subred y `WOL_SOURCE_IP` con la IP IPv4 local de la interfaz LAN; puedes consultarlos con `ip -4 addr`. No pongas `eth0` en esa variable. `WOL_PORT` vale `9`. No abras ni reenvíes ese puerto desde Internet.

### 3. Arrancar y comprobar

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now jona-homelab
sudo systemctl status jona-homelab --no-pager
curl --fail http://127.0.0.1:3000/api/health
curl -i http://127.0.0.1:3000/api/devices
sudo journalctl -u jona-homelab -n 50 --no-pager
```

La salud debe responder 200 y la consulta directa de equipos, 200 cuando se realiza localmente en el Ubuntu. Comprueba en el navegador que un usuario autorizado entra y otro queda bloqueado por Access. Registra una MAC real y envía un paquete solo cuando quieras despertar ese ordenador.

El servicio funciona sin root, sin capacidades especiales y con el sistema de archivos de solo lectura salvo su estado y directorio temporal. No actives `PrivateNetwork=true`: impediría alcanzar la LAN. No actives `MemoryDenyWriteExecute=true`: impediría el JIT de Node.

## Actualizaciones y rollback

Ejecuta actualizador incluido en release activa:

```sh
sudo /opt/jona-homelab/current/update.sh
```

El script consulta latest release y termina sin detener servicio cuando `RELEASE_VERSION` ya coincide. Si hay versión nueva, descarga paquete en staging, valida checksum, rutas, enlaces y contenido, y solo entonces detiene servicio. Con servicio parado crea un backup completo de `/var/lib/jona-homelab`, instala release bajo `/opt/jona-homelab/releases/`, cambia enlace `current` atómicamente y arranca la nueva versión. Durante ese arranque, la aplicación aplica en orden las migraciones SQLite pendientes dentro de transacciones. Después espera hasta 60 segundos a que `/api/health` responda `{"status":"ok"}`; esa respuesta confirma que inicialización y migraciones terminaron correctamente.

Actualizar no requiere eliminar ni recrear la instalación. El script no borra la base de datos ni `/etc/jona-homelab.env`, y conserva tanto el backup previo como las releases anteriores.

Consulta parámetros para instalaciones no estándar con `sudo /opt/jona-homelab/current/update.sh --help`. `--install-root`, `--data-root`, `--backup-root`, `--service`, `--health-url` y `--release-api` permiten cambiar valores predeterminados. `health-url` solo admite loopback. `/etc/jona-homelab.env`, unidad systemd, releases anteriores y backups nunca se sobrescriben ni eliminan automáticamente.

Una instalación anterior sin actualizador puede incorporarlo una vez así:

```sh
curl --fail --location --remote-name https://github.com/jonatancheca/jona-homelab/releases/latest/download/update.sh
chmod 0755 update.sh
sudo ./update.sh
```

Si nueva versión no arranca o no supera health check, actualizador detiene intento, conserva datos fallidos bajo `/var/backups/jona-homelab/failed-data-*`, restaura backup de SQLite y enlace anterior, y comprueba de nuevo servicio antiguo. El comando devuelve error aunque rollback termine correctamente, para que fallo original sea visible.

Para rollback manual, detén servicio, aparta primero directorio de datos actual completo, restaura backup compatible, apunta `current` a release anterior y arranca. No ejecutes versión antigua sobre esquema nuevo. Esta versión rechaza esquemas desconocidos y no los degrada.

## Copia y restauración

La base usa WAL. No copies solo el archivo principal mientras el servicio está activo. Para una copia sencilla y consistente, detén primero la aplicación:

```sh
sudo systemctl stop jona-homelab
sudo install -d -m 0700 /var/backups/jona-homelab
sudo tar -czf /var/backups/jona-homelab/copia-v1.tar.gz -C /var/lib jona-homelab
sudo chmod 0600 /var/backups/jona-homelab/copia-v1.tar.gz
sudo systemctl start jona-homelab
```

Usa un nombre de copia nuevo cada vez y guarda también `/etc/jona-homelab.env` mediante un medio privado. La copia incluye los archivos WAL/SHM si existen.

Para restaurar, detén la aplicación. **Aparta primero el directorio de datos actual completo** a una ubicación privada de recuperación; no mezcles una base restaurada con archivos WAL antiguos. Extrae la copia en `/var/lib`, asigna propietario `jona-homelab:jona-homelab` al directorio restaurado, permisos 0700 al directorio y 0600 a sus archivos. Arranca y comprueba los equipos. Conserva los datos apartados hasta confirmar la restauración.

## API

Cloudflare Access debe proteger todas las rutas de negocio. El backend no valida JWT ni cabeceras de origen; las mutaciones requieren `Content-Type: application/json` y un JSON de hasta 4096 bytes. No se habilita CORS. Para `DELETE` y `wake`, envía `{}`.

| Método y ruta | Entrada / resultado |
| --- | --- |
| `GET /api/devices` | Lista de equipos |
| `POST /api/devices` | `{ "name": "PC", "mac": "AA:BB:CC:DD:EE:FF" }`; 201 |
| `PATCH /api/devices/:id` | Nombre y MAC completos; 200 |
| `DELETE /api/devices/:id` | `{}`; 204 |
| `POST /api/devices/:id/wake` | `{}`; mensaje de envío, equipo y `retryAfter` |
| `GET /api/session` | Modo `development` o `access`, sin datos de identidad |
| `GET /api/health` | Salud mínima, sin datos privados |

Los equipos contienen `id`, `name`, `mac`, `createdAt`, `updatedAt` y `lastSentAt` (ISO UTC o `null`). Los errores usan 400/413/415 para entrada inválida, 401/403 para seguridad, 404 para equipo inexistente, 409 para duplicados, 429 para enfriamiento y 502 para envío fallido. Los 429 incluyen `Retry-After`. El cooldown persiste en SQLite y también se aplica a intentos fallidos. No hay reintentos automáticos de encendido.

## Fuentes técnicas

- [Despliegue Nuxt en Node](https://nuxt.com/docs/4.x/getting-started/deployment).
- [SQLite integrado, Node 24.15](https://nodejs.org/en/blog/release/v24.15.0).
- [Formato Wake-on-LAN en Ubuntu](https://manpages.ubuntu.com/manpages/resolute/man1/wakeonlan.1.html).
