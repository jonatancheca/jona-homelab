# Companion para CachyOS con KDE Plasma

## Instalar y emparejar

En CachyOS x86_64, abre una terminal en la carpeta del paquete y su checksum. La copia en la caché de pacman permite restaurar también esta primera instalación local si falla una actualización.

```bash
sha256sum -c jona-homelab-companion-cachyos-x86_64.pkg.tar.zst.sha256 && \
sudo install -m 0644 jona-homelab-companion-cachyos-x86_64.pkg.tar.zst /var/cache/pacman/pkg/ && \
sudo pacman -U /var/cache/pacman/pkg/jona-homelab-companion-cachyos-x86_64.pkg.tar.zst
```

El paquete habilita el servicio desde el arranque. Abre **Jona Homelab Companion** desde el menú de Plasma. El icono aparece automáticamente en las sesiones siguientes. Pulsa **Copiar código de emparejado**, autoriza la operación y pega el código en el dispositivo del panel, en **Companion**. Comprueba que aparece **Companion ready**. El servidor del panel necesita acceso a TCP 47654 en la IPv4 privada del equipo. Si usas un cortafuegos, permite ese puerto desde la IP del servidor. Los relojes deben estar sincronizados.

El menú del icono permite consultar la versión, rotar el código y guardar un ZIP de diagnóstico en la carpeta que elijas. Rotar el código invalida el anterior. El diagnóstico incluye direcciones locales, estado y registros del servicio. El secreto se conserva en un archivo privado de root.

## Energía y actualizaciones

El panel permite apagar, suspender e hibernar desde **Power options**. Companion consulta a logind si el equipo admite la suspensión o la hibernación. El apagado seguro respeta los inhibidores del sistema y el forzado los omite. Configura Wake-on-LAN e hibernación en el equipo si necesitas esas funciones.

**Actualizar Companion** instala el paquete de la release oficial mediante pacman y conserva el emparejado. Antes de activar la nueva versión, el actualizador verifica el SHA256 y prepara el paquete anterior. Tras reiniciar el servicio, comprueba la versión que responde y restaura la anterior si falla. Las releases publicadas comprueban actualizaciones al arrancar y cada 24 horas. El paquete inicial `local-cachyos` permite instalar la primera release compatible desde el panel por petición expresa.

## Mantenimiento

```bash
systemctl status jona-homelab-companion
journalctl -u jona-homelab-companion
sudo pacman -R jona-homelab-companion
```

La desinstalación conserva el emparejado en `/var/lib/jona-homelab-companion`. Para crear el paquete desde el repositorio, ejecuta `python companion/package-cachyos.py` con Go y Docker disponibles. El resultado queda en `artifacts/`.
