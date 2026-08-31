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

El bypass exige simultáneamente `nuxt dev`, `NODE_ENV=development`, `AUTH_DEV_BYPASS=true`, origen `http://127.0.0.1` y conexión desde loopback. El artefacto compilado nunca permite el bypass, incluso si se le pasa `NODE_ENV=development`. No uses un servidor de desarrollo como origen de un Tunnel.

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

Los tests unitarios verifican MAC, CRUD, persistencia, paquetes, errores UDP, límites y JWT. Los E2E levantan una instancia aislada en loopback, con una SQLite temporal, y mandan UDP exclusivamente a loopback. El test de producción arranca el resultado compilado y comprueba el rechazo sin Access y del bypass. No hacen broadcasts hacia ordenadores reales. Playwright y las herramientas de desarrollo **no se despliegan** al Ubuntu de producción.

Detén `pnpm dev` antes de ejecutar los E2E: Nuxt no admite dos servidores de desarrollo simultáneos sobre este mismo proyecto. Los tests usan los puertos 3123 (E2E) y 3124 (producción), que deben estar libres.

## Instalación en Ubuntu

### 1. Preparar Node y el artefacto

Instala Node 24 LTS desde la [distribución oficial de Node](https://nodejs.org/en/download). Comprueba `node --version` y `command -v node`. La unidad incluida usa `/usr/bin/node`; si la ruta difiere, sustituye **solo `ExecStart`** por la ruta absoluta del binario de Node del sistema. Evita instalaciones bajo el directorio personal: el servicio no tiene acceso a `/home`.

En el equipo de compilación, instala con el lockfile y ejecuta las validaciones. Para una entrega Ubuntu reproducible, compila en Linux con la misma arquitectura que el destino (Ubuntu/WSL o CI Linux); no copies `node_modules` de Windows.

```sh
pnpm install --frozen-lockfile
pnpm build
tar -czf jona-homelab.tar.gz -C .output .
```

Transfiere el archivo y los dos archivos de `deploy/` a Ubuntu. Solo el contenido compilado de `.output` se instala en `/opt`: no hacen falta fuentes, pnpm ni herramientas de compilación en producción.

### 2. Usuario, archivos y configuración

Ejecuta estos comandos en Ubuntu, desde la carpeta con el archivo transferido. Usa un nombre de versión **nuevo** en cada instalación; `v1` es el ejemplo inicial.

```sh
sudo useradd --system --user-group --home-dir /var/lib/jona-homelab --no-create-home --shell /usr/sbin/nologin jona-homelab
sudo install -d -m 0755 /opt/jona-homelab/releases
sudo mkdir /opt/jona-homelab/releases/v1
sudo tar -xzf jona-homelab.tar.gz -C /opt/jona-homelab/releases/v1 --no-same-owner
sudo chmod -R a+rX /opt/jona-homelab/releases/v1
sudo ln -s /opt/jona-homelab/releases/v1 /opt/jona-homelab/current
sudo install -m 0600 deploy/homelab.env.example /etc/jona-homelab.env
sudo install -m 0644 deploy/jona-homelab.service /etc/systemd/system/jona-homelab.service
sudoedit /etc/jona-homelab.env
```

Si el usuario ya existe, no vuelvas a crearlo. Los comandos de instalación de configuración son **solo para la primera instalación**: nunca sobreescribas el archivo existente durante una actualización.

Configura `APP_ORIGIN` con el origen HTTPS exacto (sin ruta), `CF_ACCESS_TEAM_DOMAIN` y el `CF_ACCESS_AUD` de la aplicación Access. `DB_PATH` apunta al archivo persistente; systemd crea `/var/lib/jona-homelab` con permisos privados. No guardes secretos ni la SQLite dentro del repositorio o del directorio de versiones.

`WOL_BROADCAST` vale `255.255.255.255` por defecto. Si el servidor tiene varias interfaces, establece el broadcast correcto de la subred y `WOL_SOURCE_IP` con la IP IPv4 local de la interfaz LAN; puedes consultarlos con `ip -4 addr`. No pongas `eth0` en esa variable. `WOL_PORT` vale `9`. No abras ni reenvíes ese puerto desde Internet.

### 3. Cloudflare Access y Tunnel

La configuración es manual: este proyecto no crea ni modifica recursos de tu cuenta.

1. Crea una aplicación Access **Self-hosted / public hostname** para el dominio completo elegido, incluyendo todas sus rutas. Crea una política **Allow** con los correos concretos autorizados, mediante tu proveedor de identidad o código de un solo uso. No uses `Everyone` ni políticas `Bypass`. Todos los usuarios permitidos podrán gestionar todos los equipos.
2. Copia el dominio de equipo y el identificador **AUD** de esa aplicación a `/etc/jona-homelab.env`. El AUD no es el token del Tunnel. La aplicación valida firma RS256, emisor, audiencia y caducidad del JWT; no confía en una cabecera de correo sin firma.
3. Sigue la [instalación oficial de Cloudflare Tunnel](https://developers.cloudflare.com/tunnel/setup/) para instalar `cloudflared` como servicio en ese mismo Ubuntu y crear un Tunnel gestionado remotamente. Trata el token del Tunnel como secreto; no lo guardes en Git, capturas o logs.
4. Publica el hostname elegido con origen **`http://127.0.0.1:3000`**. No necesitas abrir puertos entrantes del router, certificados locales ni un cliente VPN en el navegador.
5. Conserva Access protegiendo todas las rutas, desactiva reglas de caché forzada/Rocket Loader para esta aplicación y permite las conexiones salientes necesarias para `cloudflared`, DNS y HTTPS hacia las claves de Access.

Una política Access ausente no abre la aplicación: el backend rechaza solicitudes sin JWT válido. Si las claves no están disponibles y no existe una clave válida en caché, se rechaza el acceso; no hay fallback sin autenticación. `/api/health` es la única excepción local y solo devuelve `{"status":"ok"}`; Cloudflare debe proteger también esa ruta públicamente.

### 4. Arrancar y comprobar

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now jona-homelab
sudo systemctl status jona-homelab --no-pager
curl --fail http://127.0.0.1:3000/api/health
curl -i http://127.0.0.1:3000/api/devices
sudo journalctl -u jona-homelab -n 50 --no-pager
```

La salud debe responder 200 y la consulta directa de equipos, 401. Comprueba en el navegador que un usuario autorizado entra y otro queda bloqueado. Registra una MAC real y envía un paquete solo cuando quieras despertar ese ordenador.

El servicio funciona sin root, sin capacidades especiales y con el sistema de archivos de solo lectura salvo su estado y directorio temporal. No actives `PrivateNetwork=true`: impediría alcanzar la LAN. No actives `MemoryDenyWriteExecute=true`: impediría el JIT de Node.

## Actualizaciones y rollback

Compila una nueva versión, extrae el artefacto en un directorio nuevo bajo `/opt/jona-homelab/releases/` y ajusta sus permisos como en la instalación inicial. Detén el servicio, crea una copia de seguridad, cambia el enlace `current` con `sudo ln -sfn /opt/jona-homelab/releases/v2 /opt/jona-homelab/current` y vuelve a iniciarlo. Verifica salud y Access. La SQLite y `/etc/jona-homelab.env` permanecen intactos.

Para volver atrás, detén el servicio y apunta `current` a la versión anterior. No elimines versiones ni datos automáticamente. Si una futura versión cambia el esquema, no ejecutes una versión antigua sobre ese esquema: restaura también su copia compatible. Esta versión rechaza esquemas desconocidos y no los degrada.

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

Todas las rutas de negocio requieren Access. Las mutaciones requieren `Origin` exacto, `Content-Type: application/json` y un JSON de hasta 4096 bytes. No se habilita CORS. Para `DELETE` y `wake`, envía `{}`.

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
- [Validación de JWT de Cloudflare Access](https://developers.cloudflare.com/cloudflare-one/access-controls/applications/http-apps/authorization-cookie/validating-json/).
- [Formato Wake-on-LAN en Ubuntu](https://manpages.ubuntu.com/manpages/resolute/man1/wakeonlan.1.html).
